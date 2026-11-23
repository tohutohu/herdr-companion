package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/tohutohu/herdr-android-client/gateway/internal/favorites"
)

// favoriteRoutes serves the favorite working folders
// (docs/api.md#お気に入りのフォルダ).
func (s *Server) favoriteRoutes(api *http.ServeMux) {
	if s.Favorites == nil {
		return
	}
	api.HandleFunc("GET /v1/directories/favorites", s.listFavorites)
	api.HandleFunc("PUT /v1/directories/favorites", s.setFavorites)
	api.HandleFunc("POST /v1/directories/favorites", s.addFavorite)
	api.HandleFunc("DELETE /v1/directories/favorites", s.removeFavorite)
	api.HandleFunc("POST /v1/directories/favorites/import", s.importFavorites)
	if s.Projects != nil {
		api.HandleFunc("GET /v1/projects", s.listProjects)
		api.HandleFunc("PATCH /v1/projects", s.updateProject)
	}
}

// listProjects returns the favorites as projects (docs/api.md#プロジェクト).
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"projects": s.Favorites.Projects()})
}

type projectUpdate struct {
	Path   string  `json:"path"`
	Name   *string `json:"name"`
	Notify *string `json:"notify"`
}

// updateProject changes a project's name or notification level.
func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var req projectUpdate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.fail(w, r, "", "update_project", badRequest("invalid json"))
		return
	}
	if req.Path == "" {
		s.fail(w, r, "", "update_project", badRequest("path is required"))
		return
	}
	p, err := s.Favorites.Update(req.Path, req.Name, req.Notify)
	switch {
	case errors.Is(err, favorites.ErrNotFound):
		writeError(w, http.StatusNotFound, "not a project: add the folder to the favorites first")
	case err != nil:
		s.favoritesFail(w, r, "update_project", err)
	default:
		writeJSON(w, http.StatusOK, p)
	}
}

type favoritesRequest struct {
	Paths    []string `json:"paths"`
	Path     string   `json:"path"`
	Position *int     `json:"position"`
}

func decodeFavorites(r *http.Request) (favoritesRequest, error) {
	var req favoritesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		return req, badRequest("invalid json")
	}
	return req, nil
}

func (s *Server) favoritesFail(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, favorites.ErrInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.fail(w, r, "", op, err)
}

func (s *Server) listFavorites(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Favorites.List())
}

// setFavorites replaces the list. Paths are kept as given (cleaned), even a
// folder that is gone for now: reordering must not drop it.
func (s *Server) setFavorites(w http.ResponseWriter, r *http.Request) {
	req, err := decodeFavorites(r)
	if err != nil {
		s.fail(w, r, "", "set_favorites", err)
		return
	}
	snap, err := s.Favorites.Set(req.Paths)
	if err != nil {
		s.favoritesFail(w, r, "set_favorites", err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// addFavorite adds one folder. It must be one a session can start in, and is
// kept by its real path, as the folder listing shows it.
func (s *Server) addFavorite(w http.ResponseWriter, r *http.Request) {
	req, err := decodeFavorites(r)
	if err != nil {
		s.fail(w, r, "", "add_favorite", err)
		return
	}
	if req.Path == "" {
		s.fail(w, r, "", "add_favorite", badRequest("path is required"))
		return
	}
	path := req.Path
	if s.Launcher != nil {
		if path, err = s.Launcher.ResolveDir(req.Path); err != nil {
			s.fail(w, r, "", "add_favorite", err)
			return
		}
	}
	snap, err := s.Favorites.Add(path, req.Position)
	if err != nil {
		s.favoritesFail(w, r, "add_favorite", err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// removeFavorite takes ?path= out of the list. A folder that is not in it
// is no error, so two clients removing the same one both succeed.
func (s *Server) removeFavorite(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		s.fail(w, r, "", "remove_favorite", badRequest("path is required"))
		return
	}
	snap, err := s.Favorites.Remove(path)
	if err != nil {
		s.favoritesFail(w, r, "remove_favorite", err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// importFavorites moves a client's own favorites to the gateway, once: only
// while the gateway never saved a list.
func (s *Server) importFavorites(w http.ResponseWriter, r *http.Request) {
	req, err := decodeFavorites(r)
	if err != nil {
		s.fail(w, r, "", "import_favorites", err)
		return
	}
	snap, imported, err := s.Favorites.Import(req.Paths)
	if err != nil {
		s.favoritesFail(w, r, "import_favorites", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		favorites.Snapshot
		Imported bool `json:"imported"`
	}{snap, imported})
}
