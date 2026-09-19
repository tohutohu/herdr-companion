package opencode

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Run only against a disposable server with its own XDG directories. No model
// call is made: the test exercises native session creation, form replies and
// admission of a queued prompt with resume=false.
func Test公式v2サーバーとの実接続(t *testing.T) {
	endpoint := os.Getenv("HERDR_OPENCODE_TEST_URL")
	if endpoint == "" {
		t.Skip("requires isolated OpenCode v2 server")
	}
	password, err := os.ReadFile(os.Getenv("HERDR_OPENCODE_TEST_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{ServerURL: endpoint, ServerVersion: 2, Password: strings.TrimSpace(string(password)), Database: os.Getenv("HERDR_OPENCODE_TEST_DB")}, nil, nil)
	ctx := context.Background()
	c, err := p.connection()
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	cwd := os.Getenv("HERDR_OPENCODE_TEST_CWD")
	if err := p.request(ctx, c, "POST", "/api/session", cwd, map[string]any{"location": map[string]string{"directory": cwd}}, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Data.ID
	if id == "" {
		t.Fatal("missing id")
	}
	defer p.request(ctx, c, "DELETE", sessionPath(id), cwd, nil, nil)
	var f struct {
		Data form `json:"data"`
	}
	fields := []map[string]any{{"key": "color", "type": "string", "title": "Color", "options": []map[string]string{{"label": "Blue", "value": "blue-value"}}}}
	if err := p.request(ctx, c, "POST", sessionPath(id)+"/form", cwd, map[string]any{"title": "Fixture form", "fields": fields}, &f); err != nil {
		t.Fatal(err)
	}
	pending, err := p.pending(ctx, c, id, cwd)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := p.Respond(ctx, id, &providers.Live{Cwd: cwd}, model.InteractionResponse{InteractionID: pending[0].ID, Answers: map[string]model.Answer{"color": {Selected: []string{"Blue"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := p.request(ctx, c, "POST", sessionPath(id)+"/prompt", cwd, map[string]any{"text": "Fixture queued prompt", "resume": false}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Summary(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Messages(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
}
