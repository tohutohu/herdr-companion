package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
)

// historyCacheMB is 0 (the default) or within these bounds.
const minHistoryCacheMB, maxHistoryCacheMB = 64, 65536

// Run only while the gateway using this config is stopped; it reads the
// setting at start.
func setHistoryCacheCmd(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: set-history-cache MB (0 restores the default)")
	}
	mb, err := parseHistoryCacheMB(args[0])
	if err != nil {
		return err
	}
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	return store.Update(func(c *config.Config) { c.HistoryCacheMB = mb })
}

func parseHistoryCacheMB(s string) (int, error) {
	mb, err := strconv.Atoi(s)
	if err != nil || mb != 0 && (mb < minHistoryCacheMB || mb > maxHistoryCacheMB) {
		return 0, fmt.Errorf("history cache must be 0 or %d–%d MB", minHistoryCacheMB, maxHistoryCacheMB)
	}
	return mb, nil
}
