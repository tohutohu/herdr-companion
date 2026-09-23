package claude

import (
	"regexp"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// artifactURLRe finds the page a publish created or updated. The result names
// it first ("Published <file> at <url>", "Created a new Artifact at <url> from
// the Artifact type <url>", "Updated the Artifact at <url> (Version 2)").
var artifactURLRe = regexp.MustCompile(`https://claude\.ai/(?:code/)?artifact/[A-Za-z0-9_-]+`)

// artifactBlocks summarizes an Artifact call as one tool line, followed by a
// link to the published page once a publish has succeeded. Tool lines are not
// linkified by the app, so the link is a separate text block.
func artifactBlocks(in map[string]any, res toolResult, answered bool, opt ParseOptions) []model.Block {
	str := func(k string) string { s, _ := in[k].(string); return s }
	action := str("action")
	if action == "" {
		action = "publish"
	}
	if asset, _ := in["asset"].(bool); asset {
		action += " asset"
	}
	text := "▸ Artifact " + action
	target := model.DisplayPath(opt.refRoot, str("file_path"))
	for _, k := range []string{"url", "title", "intent", "type", "scope"} {
		if target != "" {
			break
		}
		target = str(k)
	}
	if target != "" {
		text += " " + target
	}
	blocks := []model.Block{model.TextBlock(text)}
	if answered && !res.block.IsError && action == "publish" {
		if url := artifactURLRe.FindString(resultText(res)); url != "" {
			blocks = append(blocks, model.TextBlock("Artifact: "+url))
		}
	}
	return blocks
}
