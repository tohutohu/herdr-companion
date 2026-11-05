package translation

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const cacheEntries = 256
const cacheLimit = 8 << 20

var workerVersion = cacheKey("worker-v1", string(workerSource))

// Translation direction, model revision and worker code all affect the result.
// Only hashes and translated text are persisted; prompt originals live separately.
func translationKey(size, target, text string) string {
	return cacheKey(workerVersion+":"+size+":"+revisions[size]+":"+target, text)
}

func (s *Service) canTranslateLocked() bool {
	return s.status.State == "ready" || s.status.State == "starting" ||
		(s.status.Supported && s.status.AutoStart)
}

func (s *Service) cacheGetLocked(key string) (string, bool) {
	v, ok := s.cache[key]
	if !ok || v.Text == "" {
		return "", false
	}
	s.cacheClock++
	v.Used = s.cacheClock
	s.cache[key] = v
	return v.Text, true
}

func (s *Service) trimCacheLocked() {
	for len(s.cache) > cacheEntries || s.cacheBytes > cacheLimit {
		var oldest string
		var used uint64
		for key, value := range s.cache {
			if oldest == "" || value.Used < used {
				oldest, used = key, value.Used
			}
		}
		s.cacheBytes -= len(s.cache[oldest].Text)
		delete(s.cache, oldest)
	}
}

func (s *Service) cachePutLocked(key, text string) {
	if len(text) > cacheLimit {
		return
	}
	s.cacheBytes -= len(s.cache[key].Text)
	s.cacheClock++
	s.cache[key] = cached{Text: text, Used: s.cacheClock}
	s.cacheBytes += len(text)
	s.trimCacheLocked()
	if err := s.saveCacheLocked(); err != nil {
		s.status.Error = "翻訳キャッシュを保存できません: " + err.Error()
	}
}

func (s *Service) loadCache() {
	f, err := os.Open(filepath.Join(s.dir, "cache.json"))
	if err != nil {
		return
	}
	defer f.Close()
	// JSON escaping can expand a character to six bytes.
	data, err := io.ReadAll(io.LimitReader(f, 6*cacheLimit+(1<<20)))
	var saved map[string]cached
	if err != nil || json.Unmarshal(data, &saved) != nil {
		return // Disposable cache: a damaged file never prevents translation.
	}
	for key, value := range saved {
		if len(key) != 64 || value.Text == "" {
			continue
		}
		s.cache[key] = value
		s.cacheBytes += len(value.Text)
		if value.Used > s.cacheClock {
			s.cacheClock = value.Used
		}
	}
	s.trimCacheLocked()
}

func (s *Service) saveCacheLocked() error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	saved := make(map[string]cached)
	for key, value := range s.cache {
		if value.Text != "" {
			saved[key] = value
		}
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "cache.json")
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
