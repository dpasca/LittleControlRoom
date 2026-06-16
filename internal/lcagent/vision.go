package lcagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/script"
)

const (
	defaultVisionProvider = "off"
	defaultVisionModel    = ""
	maxVisionImageBytes   = 25 * 1024 * 1024
)

type visionProfile struct {
	Enabled     bool
	Provider    string
	Model       string
	Message     string
	Analyzer    traceVisionAnalyzer
	DisabledErr error
}

type traceVisionAnalyzer struct {
	provider string
	client   *modeladapter.Client
}

type visionAnalyzer struct {
	profile       visionProfile
	workspaceRoot string
}

func newVisionProfile(provider string, cfg modeladapter.OpenRouterConfig, mainProvider string, mainModel string) visionProfile {
	provider, err := normalizeVisionProvider(provider)
	if err != nil {
		return visionProfile{
			Enabled:     false,
			Provider:    strings.TrimSpace(provider),
			Message:     err.Error(),
			DisabledErr: err,
		}
	}
	sameAsMain := provider == "main"
	if sameAsMain {
		provider = normalizeMainProvider(mainProvider)
		cfg.Model = firstNonEmptyString(strings.TrimSpace(cfg.Model), strings.TrimSpace(mainModel), defaultMainModelForProvider(provider))
	}
	if provider == "off" {
		return visionProfile{
			Enabled:  false,
			Provider: provider,
			Message:  "LCAgent image analysis disabled.",
		}
	}
	cfg.Model = firstNonEmptyString(strings.TrimSpace(cfg.Model), defaultMainModelForProvider(provider))
	cfg.Model = modeladapter.NormalizeModelForProvider(provider, cfg.Model)
	client, err := newChatProviderClient(provider, cfg)
	if err != nil {
		return visionProfile{
			Enabled:     false,
			Provider:    provider,
			Model:       cfg.Model,
			Message:     "LCAgent image analysis unavailable: " + err.Error(),
			DisabledErr: err,
		}
	}
	message := "LCAgent image analysis enabled."
	if sameAsMain {
		message = "LCAgent image analysis uses the Main Model."
	}
	return visionProfile{
		Enabled:  true,
		Provider: provider,
		Model:    client.Model(),
		Message:  message,
		Analyzer: traceVisionAnalyzer{provider: provider, client: client},
	}
}

func normalizeVisionProvider(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "-")
	if value == "" {
		return defaultVisionProvider, nil
	}
	switch value {
	case "main", "same", "same-as-main":
		return "main", nil
	case "off", "openrouter", "openai", "deepseek", "moonshot", "xiaomi":
		return value, nil
	default:
		return "", fmt.Errorf("vision provider must be one of: main, off, openrouter, openai, deepseek, moonshot, xiaomi")
	}
}

func (v visionAnalyzer) AnalyzeImage(ctx context.Context, request script.ImageAnalysisRequest) (script.ImageAnalysisResult, error) {
	if !v.profile.Enabled || v.profile.Analyzer.client == nil {
		return script.ImageAnalysisResult{}, fmt.Errorf("vision client is not configured")
	}
	image, err := loadVisionImage(request.Path, v.workspaceRoot)
	if err != nil {
		return script.ImageAnalysisResult{}, err
	}
	prompt := buildVisionPrompt(request, image)
	completion, err := v.profile.Analyzer.client.CompleteVision(ctx, prompt, modeladapter.ImageInput{
		MIMEType: image.MIMEType,
		Data:     image.Data,
	})
	if err != nil {
		return script.ImageAnalysisResult{}, err
	}
	modelName := firstNonEmptyString(strings.TrimSpace(completion.Model), v.profile.Model)
	return script.ImageAnalysisResult{
		Output:       strings.TrimSpace(completion.Message.Content),
		Provider:     v.profile.Provider,
		Model:        modelName,
		Usage:        json.RawMessage(completion.Usage),
		UsageSummary: completion.UsageSummary,
	}, nil
}

type visionImage struct {
	Path     string
	MIMEType string
	Data     []byte
	Bytes    int64
}

func loadVisionImage(path, workspaceRoot string) (visionImage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return visionImage{}, fmt.Errorf("image path is required")
	}
	clean, err := resolveVisionImagePath(path, workspaceRoot)
	if err != nil {
		return visionImage{}, err
	}
	info, err := os.Stat(clean)
	if err != nil {
		return visionImage{}, fmt.Errorf("read image %s: %w", path, err)
	}
	if info.IsDir() {
		return visionImage{}, fmt.Errorf("image path is a directory: %s", path)
	}
	if info.Size() <= 0 {
		return visionImage{}, fmt.Errorf("image file is empty: %s", path)
	}
	if info.Size() > maxVisionImageBytes {
		return visionImage{}, fmt.Errorf("image file is too large: %s is %d bytes, max is %d", path, info.Size(), maxVisionImageBytes)
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		return visionImage{}, fmt.Errorf("read image %s: %w", path, err)
	}
	mimeType := detectVisionMIMEType(clean, data)
	if !strings.HasPrefix(mimeType, "image/") {
		return visionImage{}, fmt.Errorf("unsupported image MIME type %q for %s", mimeType, path)
	}
	return visionImage{
		Path:     clean,
		MIMEType: mimeType,
		Data:     data,
		Bytes:    info.Size(),
	}, nil
}

func resolveVisionImagePath(path, workspaceRoot string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if filepath.IsAbs(clean) {
		return clean, nil
	}
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return clean, nil
	}
	root = filepath.Clean(root)
	target := filepath.Clean(filepath.Join(root, clean))
	if !pathUnderRoot(root, target) {
		return "", fmt.Errorf("image path escapes workspace: %s", path)
	}
	return target, nil
}

func pathUnderRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func detectVisionMIMEType(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return http.DetectContentType(data)
}

func buildVisionPrompt(request script.ImageAnalysisRequest, image visionImage) string {
	var b strings.Builder
	b.WriteString("Inspect the attached image pixels and answer the user's visual question.\n")
	fmt.Fprintf(&b, "Image path: %s\n", image.Path)
	fmt.Fprintf(&b, "Image MIME type: %s\n", image.MIMEType)
	fmt.Fprintf(&b, "Image bytes: %d\n", image.Bytes)
	if userRequest := strings.TrimSpace(request.UserRequest); userRequest != "" {
		b.WriteString("\nOriginal user request:\n")
		b.WriteString(userRequest)
		b.WriteString("\n")
	}
	if contextText := strings.TrimSpace(request.Context); contextText != "" {
		b.WriteString("\nContext / expected state:\n")
		b.WriteString(contextText)
		b.WriteString("\n")
	}
	if len(request.Checks) > 0 {
		b.WriteString("\nSpecific visual checks:\n")
		for _, check := range request.Checks {
			if check = strings.TrimSpace(check); check != "" {
				fmt.Fprintf(&b, "- %s\n", check)
			}
		}
	}
	b.WriteString("\nQuestion:\n")
	b.WriteString(strings.TrimSpace(request.Question))
	b.WriteString("\n\nRespond concisely with observed visual facts, likely issues, and any uncertainty. Do not claim to inspect code or files beyond the image.")
	return b.String()
}
