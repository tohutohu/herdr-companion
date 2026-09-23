package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Detail screens poll a session's messages every few seconds, and Claude Code
// only appends to a transcript. The cache keeps the transcripts being read
// decoded and reads only what was appended since. Image payloads are dropped
// from the kept lines (Image reads them from the file), so screenshots are
// neither held in memory nor scanned again on every conversion.
const (
	maxCachedTranscripts = 16
	// maxCachedTranscriptBytes bounds the kept lines of all cached transcripts.
	maxCachedTranscriptBytes = 64 << 20
	// imageDataLine is the line length from which image payloads are dropped;
	// shorter lines cannot hold an image worth the extra decoding.
	imageDataLine = 16 << 10
)

// messagesKey is what Messages depends on besides the transcript. Blocked
// sessions are never cached: their plan rows come from the screen.
type messagesKey struct {
	root string
	live bool
}

type cachedTranscript struct {
	mu   sync.Mutex
	info os.FileInfo
	// offset is how much of the file is decoded; it is always at a line end.
	offset int64
	// kept is the size of the decoded lines held in memory.
	kept int64
	// mtimeUpdated means t.Updated is the file time, not an entry timestamp.
	mtimeUpdated bool
	t            *Transcript
	// msgs holds converted messages for the decoded part of the file.
	msgs map[messagesKey][]model.Message
	used time.Time
}

// transcript returns the path's decoded transcript, updated to the current
// file, with its lock held; the caller unlocks it.
func (p *Provider) transcript(path, nativeID string) (*cachedTranscript, error) {
	p.transcriptMu.Lock()
	if p.transcripts == nil {
		p.transcripts = make(map[string]*cachedTranscript)
	}
	c, ok := p.transcripts[path]
	if !ok {
		c = &cachedTranscript{}
		p.transcripts[path] = c
	}
	c.used = time.Now()
	p.transcriptMu.Unlock()

	c.mu.Lock()
	if err := c.update(path, gatewayID(nativeID), p.sink); err != nil {
		c.mu.Unlock()
		p.transcriptMu.Lock()
		if p.transcripts[path] == c {
			delete(p.transcripts, path)
		}
		p.transcriptMu.Unlock()
		return nil, err
	}
	kept := c.kept
	p.transcriptMu.Lock()
	p.evictTranscripts(path, kept)
	p.transcriptMu.Unlock()
	return c, nil
}

// evictTranscripts drops the least recently used transcripts over the limits,
// never the one just read. keep's size is passed because its lock is held.
func (p *Provider) evictTranscripts(keep string, keptSize int64) {
	for {
		total := keptSize
		oldest, used := "", time.Time{}
		for path, c := range p.transcripts {
			if path == keep {
				continue
			}
			if c.mu.TryLock() {
				total += c.kept
				c.mu.Unlock()
			}
			if oldest == "" || c.used.Before(used) {
				oldest, used = path, c.used
			}
		}
		if oldest == "" || (len(p.transcripts) <= maxCachedTranscripts && total <= maxCachedTranscriptBytes) {
			return
		}
		delete(p.transcripts, oldest)
	}
}

// update decodes what was appended to the file. A replaced or truncated file
// is decoded again from the start.
func (c *cachedTranscript) update(path, sessionID string, sink deadletter.Sink) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if c.t == nil || !os.SameFile(c.info, info) || info.Size() < c.offset {
		c.t, c.offset, c.kept, c.mtimeUpdated, c.msgs = newTranscript(), 0, 0, false, nil
	}
	c.info = info
	// Like loadPath, fall back to the file time until an entry has one.
	if c.mtimeUpdated {
		c.t.Updated = time.Time{}
	}
	if info.Size() > c.offset {
		data := make([]byte, info.Size()-c.offset)
		n, err := io.ReadFull(io.NewSectionReader(f, c.offset, int64(len(data))), data)
		if err != nil && err != io.ErrUnexpectedEOF {
			return err
		}
		if used := c.decode(data[:n], sessionID, sink); used > 0 {
			c.offset += int64(used)
			c.msgs = nil
		}
	}
	c.mtimeUpdated = c.t.Updated.IsZero()
	if c.mtimeUpdated {
		c.t.Updated = info.ModTime()
	}
	return nil
}

// decode adds the complete lines in data and returns how many bytes it used.
// A last line without a newline is used only once it is valid JSON; until
// then Claude Code may still be writing it.
func (c *cachedTranscript) decode(data []byte, sessionID string, sink deadletter.Sink) int {
	used := 0
	for used < len(data) {
		end := bytes.IndexByte(data[used:], '\n')
		line := data[used:]
		if end >= 0 {
			line = data[used : used+end]
		} else if !json.Valid(bytes.TrimSpace(line)) {
			break
		}
		kept := withoutImageData(bytes.TrimSpace(line))
		c.t.decodeLine(kept, sessionID, sink)
		c.kept += int64(len(kept))
		if end < 0 {
			return len(data)
		}
		used += end + 1
	}
	return used
}

// withoutImageData returns a copy of a transcript line with base64 image
// payloads blanked. Messages and summaries only need to know that an image is
// there.
func withoutImageData(line []byte) []byte {
	if len(line) >= imageDataLine && bytes.Contains(line, []byte(`base64`)) {
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		var v any
		if d.Decode(&v) == nil && blankImageData(v) {
			if out, err := json.Marshal(v); err == nil {
				return out
			}
		}
	}
	return bytes.Clone(line)
}

// blankImageData clears image sources ({"type":"base64","data":...}) and the
// copies Claude Code records in toolUseResult ({"file":{"base64":...}}).
func blankImageData(v any) bool {
	changed := false
	switch v := v.(type) {
	case map[string]any:
		if v["type"] == "base64" {
			if s, ok := v["data"].(string); ok && s != "" {
				v["data"] = ""
				changed = true
			}
		}
		if s, ok := v["base64"].(string); ok && s != "" {
			v["base64"] = ""
			changed = true
		}
		for _, x := range v {
			changed = blankImageData(x) || changed
		}
	case []any:
		for _, x := range v {
			changed = blankImageData(x) || changed
		}
	}
	return changed
}

// messages converts the transcript, reusing the last conversion while the
// file is unchanged. The result is shared; callers must not modify it.
func (c *cachedTranscript) messages(opt ParseOptions) []model.Message {
	if opt.Live.Blocked() {
		return c.t.Messages(opt)
	}
	key := messagesKey{root: opt.Root, live: opt.Live != nil}
	if msgs, ok := c.msgs[key]; ok {
		return slices.Clone(msgs)
	}
	msgs := c.t.Messages(opt)
	if c.msgs == nil {
		c.msgs = make(map[messagesKey][]model.Message)
	}
	c.msgs[key] = msgs
	return slices.Clone(msgs)
}
