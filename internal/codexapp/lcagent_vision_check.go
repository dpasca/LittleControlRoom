package codexapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
)

// LCAgentVisionCheckResult reports the concrete provider/model that accepted
// a small image request, plus the model's short response for human inspection.
type LCAgentVisionCheckResult struct {
	Provider            string
	Model               string
	Response            string
	Verified            bool
	ObservedTopLeft     string
	ObservedBottomRight string
}

func CheckLCAgentVisionAccess(ctx context.Context, req LaunchRequest) (LCAgentVisionCheckResult, error) {
	provider, err := lcagentProviderValue(req.LCAgentProvider)
	if err != nil {
		return LCAgentVisionCheckResult{}, err
	}
	routePreset, err := lcagentRoutePresetValue(req.LCAgentRoutePreset)
	if err != nil {
		return LCAgentVisionCheckResult{}, err
	}
	mainModel := strings.TrimSpace(req.PendingModel)
	if mainModel == "" && routePreset == "" {
		mainModel = lcagentDefaultModel(provider)
	}
	if mainModel != "" && routePreset != "" && !lcagentRoutePresetMatchesModel(routePreset, mainModel) {
		routePreset = ""
	}
	if mainModel != "" && routePreset == "" {
		provider = lcagentProviderForExplicitModel(provider, mainModel)
	}
	mainProvider := firstNonEmpty(lcagentRoutePresetProvider(routePreset), provider)
	mainModel = modeladapter.NormalizeModelForProvider(mainProvider, mainModel)
	if mainModel == "" {
		mainModel = firstNonEmpty(lcagentRoutePresetModel(routePreset), lcagentDefaultModel(mainProvider))
	}

	visionProvider := lcagentVisionProviderValue(req.LCAgentVisionProvider)
	if visionProvider == "auto" {
		visionProvider = "main"
	}
	resolvedProvider := lcagentResolvedVisionProvider(routePreset, provider, visionProvider)
	if resolvedProvider == "" {
		return LCAgentVisionCheckResult{}, fmt.Errorf("LCAgent vision provider is off")
	}
	visionModel := strings.TrimSpace(req.LCAgentVisionModel)
	if visionModel == "" && visionProvider == "main" {
		visionModel = mainModel
	}
	if visionModel == "" {
		visionModel = lcagentDefaultModel(resolvedProvider)
	}
	visionModel = modeladapter.NormalizeModelForProvider(resolvedProvider, visionModel)

	cfg := LCAgentModelListConfig{
		Provider:         resolvedProvider,
		Model:            visionModel,
		EnvFile:          req.LCAgentEnvFile,
		OpenAIAPIKey:     req.LCAgentOpenAIAPIKey,
		OpenRouterAPIKey: req.LCAgentOpenRouterAPIKey,
		DeepSeekAPIKey:   req.LCAgentDeepSeekAPIKey,
		MoonshotAPIKey:   req.LCAgentMoonshotAPIKey,
		XiaomiAPIKey:     req.LCAgentXiaomiAPIKey,
		XiaomiBaseURL:    req.LCAgentXiaomiBaseURL,
		RequestTimeout:   req.LCAgentRequestTimeout,
	}
	client, err := lcagentModelListClient(resolvedProvider, cfg)
	if err != nil {
		return LCAgentVisionCheckResult{}, err
	}
	checkCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		checkCtx, cancel = context.WithTimeout(ctx, lcagentModelListTimeout(req.LCAgentRequestTimeout))
	}
	defer cancel()

	completion, err := client.CompleteVision(checkCtx, lcagentVisionCheckPrompt(), lcagentVisionCheckImage())
	if err != nil {
		return LCAgentVisionCheckResult{}, err
	}
	response := strings.TrimSpace(completion.Message.Content)
	parsed := parseLCAgentVisionCheckResponse(response)
	return LCAgentVisionCheckResult{
		Provider:            resolvedProvider,
		Model:               firstNonEmpty(strings.TrimSpace(completion.Model), visionModel),
		Response:            response,
		Verified:            parsed.CanInspectImage && normalizeVisionCheckColor(parsed.TopLeft) == "red" && normalizeVisionCheckColor(parsed.BottomRight) == "yellow",
		ObservedTopLeft:     strings.TrimSpace(parsed.TopLeft),
		ObservedBottomRight: strings.TrimSpace(parsed.BottomRight),
	}, nil
}

func lcagentVisionCheckPrompt() string {
	return strings.Join([]string{
		"This is a Little Control Room image-input connectivity check.",
		"The attached PNG is 256 by 256 pixels and contains four solid colored quadrants.",
		"Inspect the image pixels.",
		"Return only compact JSON with this schema:",
		`{"can_inspect_image":true,"top_left":"red|green|blue|yellow|unknown","bottom_right":"red|green|blue|yellow|unknown","note":"short"}`,
		"If you cannot inspect the attached image, set can_inspect_image to false and both colors to unknown.",
	}, " ")
}

type lcagentVisionCheckResponse struct {
	CanInspectImage bool   `json:"can_inspect_image"`
	TopLeft         string `json:"top_left"`
	BottomRight     string `json:"bottom_right"`
	Note            string `json:"note"`
}

func parseLCAgentVisionCheckResponse(raw string) lcagentVisionCheckResponse {
	var parsed lcagentVisionCheckResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil {
		return lcagentVisionCheckResponse{}
	}
	return parsed
}

func normalizeVisionCheckColor(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "red", "green", "blue", "yellow":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func lcagentVisionCheckImage() modeladapter.ImageInput {
	const size = 256
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := color.RGBA{R: 225, G: 45, B: 45, A: 255}
			switch {
			case x >= size/2 && y < size/2:
				c = color.RGBA{R: 40, G: 170, B: 80, A: 255}
			case x < size/2 && y >= size/2:
				c = color.RGBA{R: 45, G: 100, B: 230, A: 255}
			case x >= size/2 && y >= size/2:
				c = color.RGBA{R: 245, G: 210, B: 45, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return modeladapter.ImageInput{
		MIMEType: "image/png",
		Data:     buf.Bytes(),
	}
}
