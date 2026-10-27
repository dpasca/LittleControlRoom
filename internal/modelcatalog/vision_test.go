package modelcatalog

import "testing"

func TestVisionRoutePrefersNativeAndKeepsFallbackForOtherModels(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model, fallback, fallbackModel, wantProvider, wantModel string
		verified                                                                bool
	}{
		{"native direct", "deepseek", "deepseek-flash", "zai", "glm-5.3-flash", "main", "", false},
		{"native routed", "openrouter", "deepseek/deepseek-flash", "zai", "glm-5.3-flash", "main", "", false},
		{"explicit off", "deepseek", "deepseek-flash", "off", "glm-5.3-flash", "off", "", true},
		{"verified future model", "ollama", "future-vision", "zai", "glm-5.3-flash", "main", "", true},
		{"fallback", "deepseek", "deepseek-v4-pro", "zai", "glm-5.3-flash", "zai", "glm-5.3-flash", false},
		{"auto fallback", "deepseek", "deepseek-v4-pro", "auto", "glm-5.3-flash", "zai", "glm-5.3-flash", false},
		{"unknown capability", "ollama", "future-vision", "auto", "", "off", "", false},
		{"no prefix guessing", "deepseek", "deepseek-flash-unknown", "auto", "", "off", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, model := ResolveVisionRoute(tc.provider, tc.model, tc.fallback, tc.fallbackModel, tc.verified)
			if provider != tc.wantProvider || model != tc.wantModel {
				t.Fatalf("route = %s/%s, want %s/%s", provider, model, tc.wantProvider, tc.wantModel)
			}
		})
	}
}
