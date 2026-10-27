package lcagent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSingleModelCLIUsesSelectedModelForEveryRole(t *testing.T) {
	for _, selected := range []string{"deepseek-flash", "tomorrows-model"} {
		t.Run(selected, func(t *testing.T) {
			isolateSkillHomes(t)
			server := newModelCatalogTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != selected {
					t.Errorf("requested %q, want selected model %q", body.Model, selected)
				}
				replyHarnessTest(w, "deepseek", 1, "", harnessTestCall("final_response", map[string]any{
					"summary": "Done.", "outcome": "completed", "files_changed": []string{}, "verification": []string{},
				}))
			}))
			defer server.Close()
			t.Setenv("DEEPSEEK_BASE_URL", server.URL)
			t.Setenv("DEEPSEEK_API_KEY", "test-key")
			var stdout, stderr bytes.Buffer
			code := Run([]string{
				"exec", "--cwd", t.TempDir(), "--data-dir", t.TempDir(), "--provider", "deepseek", "--model", selected,
				"--route-preset", "single-model", "--utility-provider", "zai", "--utility-model", "glm-5.3-flash",
				"--vision-provider", "main", "--vision-model", "glm-5.3-flash", "--final-model", "old-final-model",
				"--max-turns", "2", "--output", "stream-json", "Finish directly.",
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code=%d stderr=%s\n%s", code, stderr.String(), stdout.String())
			}
			profiles := 0
			for _, line := range strings.Split(stdout.String(), "\n") {
				var event struct {
					Type     string `json:"type"`
					Provider string `json:"provider"`
					Model    string `json:"model"`
					Enabled  bool   `json:"enabled"`
				}
				if json.Unmarshal([]byte(line), &event) != nil {
					continue
				}
				if event.Type != "vision_profile" && event.Type != "search_refine_profile" {
					continue
				}
				profiles++
				if !event.Enabled || event.Provider != "deepseek" || event.Model != selected {
					t.Fatalf("role escaped selected model: %+v", event)
				}
			}
			if profiles != 2 {
				t.Fatalf("missing role profiles: %s", stdout.String())
			}
		})
	}
}
