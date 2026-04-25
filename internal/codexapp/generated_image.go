package codexapp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/appfs"
)

const generatedImageArtifactRootName = "generated-images"

func (s *appServerSession) renderThreadItemForTurn(turnStatus string, item map[string]json.RawMessage) (string, TranscriptKind, string, *GeneratedImageArtifact) {
	itemID, kind, text := renderResumedThreadItemForTurn(turnStatus, item)
	if decodeRawString(item["type"]) != "imageGeneration" {
		return itemID, kind, text, nil
	}
	image := materializeGeneratedImageArtifact(s.dataDir, s.threadID, item)
	return itemID, TranscriptTool, renderImageGenerationTranscriptText(item, image), image
}

func materializeGeneratedImageArtifact(dataDir, threadID string, item map[string]json.RawMessage) *GeneratedImageArtifact {
	data, sourcePath := generatedImageSourceData(item)
	if len(data) == 0 {
		return nil
	}

	width, height, format := generatedImageConfig(data)
	id := generatedImageID(item, data)
	ext := generatedImageExtension(format, sourcePath)
	stablePath := generatedImageArtifactPath(dataDir, threadID, id, ext)
	if stablePath != "" {
		if existing, err := os.ReadFile(stablePath); err == nil && len(existing) > 0 {
			data = existing
			width, height, _ = generatedImageConfig(data)
		} else if err := os.MkdirAll(filepath.Dir(stablePath), 0o700); err == nil {
			if err := os.WriteFile(stablePath, data, 0o600); err != nil {
				stablePath = ""
			}
		} else {
			stablePath = ""
		}
	}

	return &GeneratedImageArtifact{
		ID:          id,
		Path:        stablePath,
		SourcePath:  sourcePath,
		Width:       width,
		Height:      height,
		ByteSize:    int64(len(data)),
		PreviewData: append([]byte(nil), data...),
	}
}

func renderImageGenerationTranscriptText(item map[string]json.RawMessage, image *GeneratedImageArtifact) string {
	status := strings.TrimSpace(decodeRawString(item["status"]))
	if image != nil {
		lines := []string{"Generated image"}
		if strings.TrimSpace(image.Path) != "" {
			lines = append(lines, image.Path)
		}
		return strings.Join(lines, "\n")
	}
	if status == "" {
		return "Image generation"
	}
	return "Image generation [" + status + "]"
}

func generatedImageSourceData(item map[string]json.RawMessage) ([]byte, string) {
	if encoded := strings.TrimSpace(decodeRawString(item["result"])); encoded != "" {
		if comma := strings.IndexByte(encoded, ','); strings.HasPrefix(encoded, "data:") && comma >= 0 {
			encoded = encoded[comma+1:]
		}
		if data, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(data) > 0 {
			return data, generatedImageSourcePath(item)
		}
	}
	sourcePath := generatedImageSourcePath(item)
	if sourcePath == "" {
		return nil, ""
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil || len(data) == 0 {
		return nil, sourcePath
	}
	return data, sourcePath
}

func generatedImageSourcePath(item map[string]json.RawMessage) string {
	for _, key := range []string{"savedPath", "saved_path", "path"} {
		if path := strings.TrimSpace(decodeRawString(item[key])); path != "" {
			return path
		}
	}
	return ""
}

func generatedImageID(item map[string]json.RawMessage, data []byte) string {
	if id := safeGeneratedImageName(generatedImageItemID(item)); id != "" {
		return id
	}
	sum := sha256.Sum256(data)
	return "image-" + hex.EncodeToString(sum[:])[:16]
}

func generatedImageItemID(item map[string]json.RawMessage) string {
	for _, key := range []string{"id", "callId", "call_id"} {
		if id := strings.TrimSpace(decodeRawString(item[key])); id != "" {
			return id
		}
	}
	return ""
}

func generatedImageArtifactPath(dataDir, threadID, id, ext string) string {
	id = safeGeneratedImageName(id)
	if id == "" {
		id = "image"
	}
	threadID = safeGeneratedImageName(threadID)
	if threadID == "" {
		threadID = "unknown-thread"
	}
	if ext == "" {
		ext = ".png"
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = appfs.DefaultDataDir()
	}
	return filepath.Join(filepath.Clean(dataDir), "artifacts", generatedImageArtifactRootName, threadID, id+ext)
}

func generatedImageConfig(data []byte) (int, int, string) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, ""
	}
	return config.Width, config.Height, strings.ToLower(strings.TrimSpace(format))
}

func generatedImageExtension(format, sourcePath string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "jpeg":
		return ".jpg"
	case "png", "gif", "webp":
		return "." + format
	}
	if ext := strings.ToLower(filepath.Ext(strings.TrimSpace(sourcePath))); ext != "" {
		return ext
	}
	return ".png"
}

func safeGeneratedImageName(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var out strings.Builder
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '-' || r == '_':
			out.WriteRune(r)
		}
		if out.Len() >= 96 {
			break
		}
	}
	return out.String()
}
