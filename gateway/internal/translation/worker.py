"""Private JSON-lines MLX worker. Only model downloads use the network."""
import contextlib
import json
import re
import sys

PROTOCOL = sys.stdout
REFERENCE_TOKENS = 2048
CONTEXT_TOKENS = 512


def reference_text(text):
    # Reference data cannot introduce PFML3 fields. Current-source literals are
    # protected separately and restored exactly, like code and URLs.
    return text.replace("<|plamo:", "＜|plamo:")


def build_prompt(source, target, history, context, tokenizer):
    output_tag = "<|plamo:tag|>output<|plamo:key|>lang<|plamo:val|>" + target + "<|plamo:msg|>"
    input_tag = "<|plamo:tag|>input<|plamo:msg|>"
    context = reference_text(context)
    # Trim text at character boundaries, using actual tokenizer counts. Keep
    # both ends so the latest proposal survives a long reference excerpt.
    while context and len(tokenizer.encode(context)) > CONTEXT_TOKENS:
        keep = max(0, len(context) * 3 // 4 - 5)
        context = context[:keep // 2] + " […] " + context[-(keep - keep // 2):] if keep else ""
    reference = "<|plamo:tag|>context<|plamo:msg|>" + context if context else ""
    pairs = []
    for previous_source, previous_translation in reversed(history[-2:]):
        pair = input_tag + reference_text(previous_source) + output_tag + reference_text(previous_translation)
        if len(tokenizer.encode(reference + pair + "".join(pairs))) > REFERENCE_TOKENS:
            break  # Drop whole pairs; never present misaligned partial translations.
        pairs.insert(0, pair)
    glossary = "".join("<|plamo:index|>" + literal + "<|plamo:item|>" + literal
                       for literal in re.findall(r"HERDRLITERALX*\d+END", source))
    if glossary:
        glossary = "<|plamo:tag|>glossary" + glossary
    return ("<|plamo:tag|>dataset<|plamo:msg|>translation" + reference +
            "".join(pairs) + glossary + input_tag + source + output_tag)

def emit(**value):
    print(json.dumps(value, ensure_ascii=False), file=PROTOCOL, flush=True)


def pieces(text, limit=1800):
    # Fenced code, inline code and URLs are passed through byte for byte.
    pattern = r"(?ms)(^[ \t]*(`{3,}|~{3,})[^\n]*\n.*?(?:^[ \t]*\2[^\n]*(?:\n|$)|\Z))|(?m:((?:^(?: {4}|\t)[^\n]*(?:\n|$))+))"
    start = 0
    for match in re.finditer(pattern, text):
        yield from prose(text[start:match.start()], limit)
        yield False, match.group()
        start = match.end()
    yield from prose(text[start:], limit)


def prose(text, limit):
    while text:
        if len(text) <= limit:
            yield True, text
            break
        cut = max(text.rfind("\n", 0, limit), text.rfind("。", 0, limit), text.rfind(". ", 0, limit), text.rfind(" ", 0, limit))
        cut = cut + 1 if cut > limit // 2 else limit
        yield True, text[:cut]
        text = text[cut:]


def translate(text, target, run):
    try:
        if isinstance(json.loads(text), (dict, list)):
            return text
    except (ValueError, TypeError):
        pass
    result = []
    history = []  # Request-local: never carry one session's text into another.
    for should_translate, part in pieces(text):
        if not should_translate or not part.strip():
            result.append(part)
            continue
        leading = part[:len(part) - len(part.lstrip())]
        trailing = part[len(part.rstrip()):]
        literals = []
        prefix = "HERDRLITERAL"
        while prefix in part:
            prefix += "X"
        def protect(match):
            literals.append(match.group())
            return prefix + str(len(literals) - 1) + "END"
        protected = re.sub(r"(`+)[^`\n]+\1|https?://[^\s<>]+|<\|plamo:[^\n]*?\|>", protect, part.strip())
        translated = run(protected, target, tuple(history))
        for index, literal in enumerate(literals):
            token = prefix + str(index) + "END"
            if translated.count(token) != 1:
                raise ValueError("code or URL placeholder was changed; no partial translation was sent")
            translated = translated.replace(token, literal)
        # Store restored literals, not reusable placeholder IDs: the next chunk
        # may use HERDRLITERAL0END for an entirely different path.
        history = (history + [(part.strip(), translated)])[-2:]
        result.append(leading + translated + trailing)
    return "".join(result)


def main():
    action, path = sys.argv[1:3]
    if action == "download":
        from huggingface_hub import snapshot_download
        snapshot_download(sys.argv[3], revision=sys.argv[4], local_dir=path,
                          ignore_patterns=["figures/*", "*.md", "serve.py", "server.py", "serving_memory.py"])
        return
    # Libraries and model code must never write into the protocol stream.
    with contextlib.redirect_stdout(sys.stderr):
        from mlx_lm import load, stream_generate
        from mlx_lm.sample_utils import make_sampler
        model, tokenizer = load(path, trust_remote_code=True, tokenizer_config={"trust_remote_code": True})
    emit(ready=True)
    for line in sys.stdin:
        try:
            request = json.loads(line)
            if request["target"] not in ("ja", "en"):
                raise ValueError("unsupported target")
            def run(source, target, history):
                # PFN recommends direct PFML3 prompts for application integration.
                prompt = build_prompt(source, target, history, request.get("context", ""), tokenizer)
                tokens = tokenizer.encode(prompt)
                if len(tokens) + 4096 > 32768:
                    raise ValueError("translation input exceeds context limit")
                output = []
                with contextlib.redirect_stdout(sys.stderr):
                    for response in stream_generate(model, tokenizer, prompt=prompt, max_tokens=4096, sampler=make_sampler(temp=0)):
                        output.append(response.text)
                        if response.finish_reason == "length":
                            raise ValueError("translation exceeded generation limit; no partial translation was sent")
                text = "".join(output).split("<|plamo:tag|>")[0].split("<|plamo:bos|>")[0].strip()
                if not text:
                    raise ValueError("empty translation")
                return text
            emit(text=translate(request["text"], request["target"], run))
        except Exception as error:
            emit(error=str(error))

if __name__ == "__main__":
    main()
