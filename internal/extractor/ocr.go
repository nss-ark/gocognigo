package extractor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// OCRConfig controls which OCR provider to use for scanned PDFs.
type OCRConfig struct {
	Provider    string // "tesseract", "sarvam", or "" (none)
	SarvamKey   string // Sarvam Vision API key
	TesseractOk bool   // cached: true if tesseract is on PATH
}

// DetectTesseract checks whether the tesseract binary is available.
func DetectTesseract() bool {
	cmd := exec.Command("tesseract", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// RunOCR attempts OCR on a PDF that yielded no extractable text.
// It tries the configured provider first, then falls back to the other.
func RunOCR(cfg OCRConfig, pdfPath string) ([]DocumentChunk, error) {
	fileName := filepath.Base(pdfPath)

	switch strings.ToLower(cfg.Provider) {
	case "tesseract":
		chunks, err := tesseractOCR(pdfPath, fileName)
		if err != nil && cfg.SarvamKey != "" {
			log.Printf("Tesseract OCR failed for %s, falling back to Sarvam: %v", fileName, err)
			return sarvamOCR(pdfPath, fileName, cfg.SarvamKey)
		}
		return chunks, err

	case "sarvam":
		chunks, err := sarvamOCR(pdfPath, fileName, cfg.SarvamKey)
		if err != nil && cfg.TesseractOk {
			log.Printf("Sarvam OCR failed for %s, falling back to Tesseract: %v", fileName, err)
			return tesseractOCR(pdfPath, fileName)
		}
		return chunks, err

	default:
		// Try tesseract if available, else sarvam
		if cfg.TesseractOk {
			return tesseractOCR(pdfPath, fileName)
		}
		if cfg.SarvamKey != "" {
			return sarvamOCR(pdfPath, fileName, cfg.SarvamKey)
		}
		return nil, fmt.Errorf("no OCR provider available (install tesseract or configure Sarvam API key)")
	}
}

// ==========================================
// Tesseract CLI OCR
// ==========================================
//
// Tesseract can't read PDFs directly, so we first convert
// each page to a PNG using pdftoppm (from Poppler) or
// magick (ImageMagick), then run tesseract on each image.
//
// If neither converter is available, we try tesseract on the
// raw file hoping the user's tesseract version handles it.

func tesseractOCR(pdfPath, fileName string) ([]DocumentChunk, error) {
	// Create temp directory for images
	tmpDir, err := os.MkdirTemp("", "gocognigo-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Step 1: Convert PDF pages to images
	imagePrefix := filepath.Join(tmpDir, "page")
	converted := false

	// Try pdftoppm (Poppler) first — best quality, fastest
	if _, err := exec.LookPath("pdftoppm"); err == nil {
		cmd := exec.Command("pdftoppm", "-png", "-r", "300", pdfPath, imagePrefix)
		if err := cmd.Run(); err == nil {
			converted = true
		}
	}

	// Try ImageMagick as fallback
	if !converted {
		if magickPath, err := exec.LookPath("magick"); err == nil {
			cmd := exec.Command(magickPath, "convert", "-density", "300", pdfPath, imagePrefix+"-%03d.png")
			if err := cmd.Run(); err == nil {
				converted = true
			}
		}
	}

	if !converted {
		return nil, fmt.Errorf("cannot convert PDF to images: install Poppler (pdftoppm) or ImageMagick (magick)")
	}

	// Step 2: Find all generated page images
	pattern := imagePrefix + "*"
	imageFiles, err := filepath.Glob(pattern)
	if err != nil || len(imageFiles) == 0 {
		return nil, fmt.Errorf("no page images generated from PDF")
	}

	// Sort image files to maintain page order
	sortImageFiles(imageFiles)

	// Step 3: Run tesseract on each page image
	var chunks []DocumentChunk
	for i, imgFile := range imageFiles {
		pageNum := i + 1

		cmd := exec.Command("tesseract", imgFile, "stdout", "-l", "eng", "--psm", "3")
		var out bytes.Buffer
		cmd.Stdout = &out

		if err := cmd.Run(); err != nil {
			log.Printf("Tesseract failed on page %d of %s: %v", pageNum, fileName, err)
			continue
		}

		text := strings.TrimSpace(out.String())
		if len(text) > 20 { // skip near-empty pages
			chunks = append(chunks, DocumentChunk{
				PageNumber: pageNum,
				Document:   fileName,
				Text:       text,
			})
		}
	}

	if len(chunks) == 0 {
		return nil, fmt.Errorf("tesseract OCR extracted no text from %s", fileName)
	}

	log.Printf("Tesseract OCR extracted %d pages from %s", len(chunks), fileName)
	return chunks, nil
}

// sortImageFiles sorts image file paths by the page number embedded in the filename.
func sortImageFiles(files []string) {
	re := regexp.MustCompile(`(\d+)\.png$`)
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			ni := extractNum(files[i], re)
			nj := extractNum(files[j], re)
			if ni > nj {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
}

func extractNum(path string, re *regexp.Regexp) int {
	m := re.FindStringSubmatch(filepath.Base(path))
	if len(m) >= 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// ==========================================
// Sarvam Vision OCR
// ==========================================

func sarvamOCR(pdfPath, fileName, apiKey string) ([]DocumentChunk, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("sarvam API key not configured")
	}

	// Read the PDF file and base64-encode it
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read PDF for Sarvam OCR: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(data)

	// Call Sarvam Vision OCR API
	reqBody, _ := json.Marshal(map[string]interface{}{
		"image":         "data:application/pdf;base64," + b64,
		"model":         "sarvam-ocr-1",
		"language_hint": "en",
	})

	req, _ := http.NewRequest("POST", "https://api.sarvam.ai/v1/vision/ocr", bytes.NewBuffer(reqBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sarvam API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sarvam API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response — Sarvam returns pages with text
	var result struct {
		Pages []struct {
			PageNumber int    `json:"page_number"`
			Text       string `json:"text"`
		} `json:"pages"`
		Text string `json:"text"` // some endpoints return full text
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse Sarvam response: %w", err)
	}

	var chunks []DocumentChunk

	if len(result.Pages) > 0 {
		for _, p := range result.Pages {
			if len(strings.TrimSpace(p.Text)) > 0 {
				chunks = append(chunks, DocumentChunk{
					PageNumber: p.PageNumber,
					Document:   fileName,
					Text:       p.Text,
				})
			}
		}
	} else if len(result.Text) > 0 {
		// Fallback: single text block
		chunks = append(chunks, DocumentChunk{
			PageNumber: 1,
			Document:   fileName,
			Text:       result.Text,
		})
	}

	if len(chunks) == 0 {
		return nil, fmt.Errorf("sarvam OCR returned no text for %s", fileName)
	}

	log.Printf("Sarvam OCR extracted %d pages from %s", len(chunks), fileName)
	return chunks, nil
}
