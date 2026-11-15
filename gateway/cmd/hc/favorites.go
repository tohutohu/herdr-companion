package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
)

// favorites is what the Gateway answers on /v1/directories/favorites.
type favorites struct {
	Paths []string `json:"paths"`
	Saved bool     `json:"saved"`
}

// errNoFavorites is what an older Gateway, without favorite folders, gives.
var errNoFavorites = errors.New("this Gateway does not keep favorite folders (update the Gateway)")

const favoritesPath = "/v1/directories/favorites"

var errNoFavoriteMatch = errors.New("no favorite folder matches")

// cmdFav manages the favorite folders the Gateway keeps for every client:
// the projects the user starts sessions in.
func (a *app) cmdFav(args []string) error {
	if len(args) == 0 {
		return a.favList(nil)
	}
	sub, rest := args[0], args[1:]
	cmds := map[string]func([]string) error{
		"ls": a.favList, "list": a.favList, "add": a.favAdd, "rm": a.favRemove, "remove": a.favRemove, "mv": a.favMove, "move": a.favMove,
	}
	run, ok := cmds[sub]
	if !ok {
		return usagef("unknown fav subcommand %q (ls, add, rm, mv)", sub)
	}
	return run(rest)
}

func (a *app) favDo(ctx context.Context, method, path string, body any) (favorites, error) {
	var res favorites
	err := a.client.do(ctx, method, path, body, &res)
	var ae *apiError
	if errors.As(err, &ae) && (ae.status == http.StatusNotFound || ae.status == http.StatusMethodNotAllowed) && path == favoritesPath {
		return res, errNoFavorites
	}
	return res, err
}

func (a *app) loadFavorites(ctx context.Context) ([]string, error) {
	res, err := a.favDo(ctx, http.MethodGet, favoritesPath, nil)
	return res.Paths, err
}

func (a *app) favList(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("fav ls", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("fav ls takes no arguments")
	}
	res, err := a.favDo(context.Background(), http.MethodGet, favoritesPath, nil)
	if err != nil {
		return err
	}
	return a.printFavorites(res)
}

func (a *app) printFavorites(res favorites) error {
	if a.json {
		if res.Paths == nil {
			res.Paths = []string{}
		}
		return a.printJSON(res)
	}
	if len(res.Paths) == 0 {
		fmt.Fprintln(a.out, "no favorite folders (hc fav add DIR)")
		return nil
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	for i, p := range res.Paths {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", i+1, filepath.Base(p), p)
	}
	return tw.Flush()
}

func (a *app) favAdd(args []string) error {
	fs := flag.NewFlagSet("fav add", flag.ContinueOnError)
	at := fs.Int("at", 0, "position, 1 = first (default: last)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usagef("fav add takes one folder")
	}
	dir := ""
	if len(pos) == 1 {
		dir = pos[0]
	}
	if dir, err = a.localDir(dir); err != nil {
		return err
	}
	body := map[string]any{"path": dir}
	if *at > 0 {
		body["position"] = *at - 1
	}
	res, err := a.favDo(context.Background(), http.MethodPost, favoritesPath, body)
	if err != nil {
		return err
	}
	return a.printFavorites(res)
}

// localDir is DIR as the Gateway's machine sees it: the current directory
// when empty, made absolute when hc runs on that machine.
func (a *app) localDir(dir string) (string, error) {
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	if !a.client.conn.local {
		return "", usagef("give an absolute folder on the Gateway's machine: hc talks to a Gateway on another machine")
	}
	if dir == "" {
		return os.Getwd()
	}
	return filepath.Abs(dir)
}

// findFavorite finds FAV in the list: a position (1 = first), a path, or a
// folder name that only one favorite has.
func findFavorite(list []string, ref string) (string, error) {
	if n, err := strconv.Atoi(ref); err == nil {
		if n < 1 || n > len(list) {
			return "", fmt.Errorf("there is no favorite %d (hc fav ls lists %d)", n, len(list))
		}
		return list[n-1], nil
	}
	clean := filepath.Clean(ref)
	var named []string
	for _, p := range list {
		if p == clean {
			return p, nil
		}
		if filepath.Base(p) == ref {
			named = append(named, p)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return "", fmt.Errorf("%w %q (see \"hc fav ls\")", errNoFavoriteMatch, ref)
	}
	return "", fmt.Errorf("%q names %d favorites: %s; give the path or its number", ref, len(named), strings.Join(named, ", "))
}

func (a *app) favRemove(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("fav rm", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("fav rm needs a favorite: its number, name or path")
	}
	ctx := context.Background()
	list, err := a.loadFavorites(ctx)
	if err != nil {
		return err
	}
	// Resolve every argument first, so numbers refer to the list as it was shown.
	var paths []string
	for _, ref := range pos {
		p, err := findFavorite(list, ref)
		if err != nil {
			return err
		}
		paths = append(paths, p)
	}
	var res favorites
	for _, p := range paths {
		if res, err = a.favDo(ctx, http.MethodDelete, favoritesPath+"?path="+url.QueryEscape(p), nil); err != nil {
			return err
		}
	}
	return a.printFavorites(res)
}

func (a *app) favMove(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("fav mv", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usagef("fav mv takes a favorite and its new position: hc fav mv app 1")
	}
	to, err := strconv.Atoi(pos[1])
	if err != nil || to < 1 {
		return usagef("fav mv: the position must be 1 or more, not %q", pos[1])
	}
	ctx := context.Background()
	list, err := a.loadFavorites(ctx)
	if err != nil {
		return err
	}
	p, err := findFavorite(list, pos[0])
	if err != nil {
		return err
	}
	// POST with a position moves the folder without sending the whole list, so
	// a change another client made meanwhile is kept.
	res, err := a.favDo(ctx, http.MethodPost, favoritesPath, map[string]any{"path": p, "position": to - 1})
	if err != nil {
		return err
	}
	return a.printFavorites(res)
}

// resolveCwd turns a --cwd value into a folder on the Gateway's machine. An
// absolute path is kept. A relative one is a folder here when hc runs on
// that machine and it exists, else the name of a favorite folder
// ("--cwd herdr-android-client").
func (a *app) resolveCwd(ctx context.Context, dir string) (string, error) {
	if dir == "" || filepath.IsAbs(dir) {
		return dir, nil
	}
	var local string
	if a.client.conn.local {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			return abs, nil
		}
		local = abs
	}
	list, err := a.loadFavorites(ctx)
	if err != nil && !errors.Is(err, errNoFavorites) {
		return "", err
	}
	// Positions are left out: "--cwd 2" reads as a folder named 2.
	if _, nerr := strconv.Atoi(dir); nerr != nil {
		p, ferr := findFavorite(list, dir)
		if ferr == nil {
			return p, nil
		}
		if !errors.Is(ferr, errNoFavoriteMatch) {
			return "", ferr
		}
	}
	if local != "" {
		return local, nil // the Gateway says what is wrong with it
	}
	return "", usagef("--cwd %q is neither an absolute folder on the Gateway's machine nor a favorite (hc fav ls)", dir)
}

// taskCwd resolves a resident task's --cwd like hc start's.
func (a *app) taskCwd(dir string) (string, error) {
	if dir == "" {
		return "", usagef("--cwd needs a folder")
	}
	return a.resolveCwd(context.Background(), dir)
}
