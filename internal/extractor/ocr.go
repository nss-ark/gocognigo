package extractor

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OCRConfig controls which OCR provider to use for scanned PDFs.
type OCRConfig struct {
	Provider    string // "tesseract", "sarvam", or "" (none)
	SarvamKey   string // Sarvam Document Intelligence API subscription key
	TesseractOk bool   // cached: true if tesseract was found
}

// tesseractBin holds the resolved path to the tesseract binary.
// Set by DetectTesseract(). May be just "tesseract" if on PATH,
// or a full path like "C:\Program Files\Tesseract\tesseract.exe".
var tesseractBin string

// DetectTesseract checks whether the tesseract binary is available.
// It first checks PATH, then probes common Windows install directories.
func DetectTesseract() bool {
	// 1. Check if tesseract is on PATH
	if path, err := exec.LookPath("tesseract"); err == nil {
		// Verify eng.traineddata exists in this installation's tessdata
		tessdataDir := filepath.Join(filepath.Dir(path), "tessdata")
		engPath := filepath.Join(tessdataDir, "eng.traineddata")
		if _, statErr := os.Stat(engPath); statErr == nil {
			tesseractBin = path
			log.Printf("Tesseract found on PATH: %s", path)
			return true
		}
		log.Printf("Tesseract on PATH at %s but eng.traineddata missing at %s, checking other locations...", path, tessdataDir)
	}

	// 2. On Windows, check common installation directories
	if runtime.GOOS == "windows" {
		candidates := []string{
			`C:\Program Files\Tesseract-OCR\tesseract.exe`,
			`C:\Program Files\Tesseract\tesseract.exe`,
			`C:\Program Files (x86)\Tesseract-OCR\tesseract.exe`,
			`C:\Program Files (x86)\Tesseract\tesseract.exe`,
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Tesseract-OCR", "tesseract.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Tesseract-OCR", "tesseract.exe"),
		}
		// Also check local tools/ directory (auto-installed by start.ps1)
		if exePath, err := os.Executable(); err == nil {
			projectDir := filepath.Dir(exePath)
			candidates = append(candidates,
				filepath.Join(projectDir, "tools", "tesseract", "tesseract.exe"),
				filepath.Join(projectDir, "tools", "Tesseract-OCR", "tesseract.exe"),
			)
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				// Verify it actually runs
				cmd := exec.Command(c, "--version")
				if err := cmd.Run(); err == nil {
					// Verify eng.traineddata exists
					tessdataDir := filepath.Join(filepath.Dir(c), "tessdata")
					engPath := filepath.Join(tessdataDir, "eng.traineddata")
					if _, statErr := os.Stat(engPath); statErr != nil {
						log.Printf("Tesseract at %s: skipping, eng.traineddata not found at %s", c, tessdataDir)
						continue
					}
					tesseractBin = c
					log.Printf("Tesseract found at: %s (tessdata: %s)", c, tessdataDir)
					return true
				}
			}
		}
	}

	log.Printf("Tesseract OCR not found (install tesseract for scanned PDF support)")
	return false
}

// DetectPdftoppm checks whether pdftoppm (Poppler) or magick (ImageMagick)
// is available for converting PDFs to images (required by Tesseract OCR).
func DetectPdftoppm() bool {
	if _, err := exec.LookPath("pdftoppm"); err == nil {
		return true
	}
	if _, err := exec.LookPath("magick"); err == nil {
		return true
	}
	return false
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

// tesseractSem is a semaphore to limit concurrent Tesseract processes.
// Tesseract itself can be CPU-intensive, and running too many instances
// concurrently can lead to thrashing or out-of-memory issues.
// Initialized to the number of CPU cores.
var tesseractSem = make(chan struct{}, runtime.NumCPU())

// tesseractOCR runs Tesseract on a PDF by first converting it to images.
// It limits concurrency across all active extractions to prevent CPU thrashing.
func tesseractOCR(pdfPath, fileName string) ([]DocumentChunk, error) {
	bin := tesseractBin
	if bin == "" {
		return nil, fmt.Errorf("tesseract binary not found")
	}

	// Resolve tessdata directory — TESSDATA_PREFIX must point directly
	// to the tessdata/ directory itself (containing eng.traineddata).
	// Tesseract v5.5 looks for: TESSDATA_PREFIX/eng.traineddata
	tesseractDir := filepath.Dir(bin)
	tessDataPrefix := filepath.Join(tesseractDir, "tessdata")

	// Create temp directory for images
	tmpDir, err := os.MkdirTemp("", "gocognigo-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Step 1: Convert PDF pages to images
	imagePrefix := filepath.Join(tmpDir, "page")
	converted := false
	var convertErr error

	// Try pdftoppm (Poppler) first — best quality, fastest
	if pdftoppmPath, lookErr := exec.LookPath("pdftoppm"); lookErr == nil {
		cmd := exec.Command(pdftoppmPath, "-png", "-r", "200", pdfPath, imagePrefix)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			converted = true
			log.Printf("Converted %s to images using pdftoppm", fileName)
		} else {
			convertErr = fmt.Errorf("pdftoppm: %v (stderr: %s)", err, stderr.String())
		}
	}

	// Try ImageMagick as fallback
	if !converted {
		if magickPath, lookErr := exec.LookPath("magick"); lookErr == nil {
			cmd := exec.Command(magickPath, "convert", "-density", "200", pdfPath, imagePrefix+"-%03d.png")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil {
				converted = true
				log.Printf("Converted %s to images using ImageMagick", fileName)
			} else {
				convertErr = fmt.Errorf("magick: %v (stderr: %s)", err, stderr.String())
			}
		}
	}

	if !converted {
		errMsg := "install Poppler (pdftoppm) or ImageMagick (magick)"
		if convertErr != nil {
			errMsg = convertErr.Error()
		}
		return nil, fmt.Errorf("cannot convert PDF to images: %s", errMsg)
	}

	// Step 2: Find all generated page images
	pattern := imagePrefix + "*"
	imageFiles, err := filepath.Glob(pattern)
	if err != nil || len(imageFiles) == 0 {
		return nil, fmt.Errorf("no page images generated from PDF")
	}

	// Sort image files to maintain page order
	sortImageFiles(imageFiles)

	// Step 3: Run tesseract on each page image concurrently
	var chunks []DocumentChunk
	var chunkMu sync.Mutex
	var firstErrLogged sync.Once

	var wg sync.WaitGroup
	for i, imgFile := range imageFiles {
		wg.Add(1)
		go func(idx int, file string) {
			defer wg.Done()

			// Acquire global Tesseract slot
			tesseractSem <- struct{}{}
			defer func() { <-tesseractSem }()

			pageNum := idx + 1
			cmd := exec.Command(bin, file, "stdout", "-l", "eng", "--psm", "6")
			cmd.Env = append(os.Environ(),
				"TESSDATA_PREFIX="+tessDataPrefix,
				"OMP_THREAD_LIMIT=1", // disable Tesseract internal multithreading to avoid CPU thrashing
			)
			var out bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				firstErrLogged.Do(func() {
					log.Printf("Tesseract failed on page %d of %s: %v | stderr: %s", pageNum, fileName, err, strings.TrimSpace(stderr.String()))
				})
				return
			}

			text := strings.TrimSpace(out.String())
			if len(text) > 20 { // skip near-empty pages
				chunkMu.Lock()
				chunks = append(chunks, DocumentChunk{
					PageNumber: pageNum,
					Document:   fileName,
					Text:       text,
				})
				chunkMu.Unlock()
			}
		}(i, imgFile)
	}

	wg.Wait()

	// Sort chunks by page number since they completed concurrently
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].PageNumber < chunks[j].PageNumber
	})

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
// Sarvam Document Intelligence OCR
// ==========================================
//
// New job-based API flow:
// 1. Create job  → POST /doc-digitization/job/v1
// 2. Get upload URL → POST /doc-digitization/job/v1/upload-files
// 3. Upload PDF  → PUT <presigned_url>
// 4. Start job   → POST /doc-digitization/job/v1/:job_id/start
// 5. Poll status → GET /doc-digitization/job/v1/:job_id/status
// 6. Download    → POST /doc-digitization/job/v1/:job_id/download-files
// 7. Parse output → extract text from markdown

const sarvamBaseURL = "https://api.sarvam.ai/doc-digitization/job/v1"

// sarvamMu serializes Sarvam API requests to avoid triggering circuit breakers.
var sarvamMu sync.Mutex

// sarvamCircuitBreakerErr indicates a retriable circuit breaker error.
type sarvamCircuitBreakerErr struct{ msg string }

func (e *sarvamCircuitBreakerErr) Error() string { return e.msg }

func sarvamOCR(pdfPath, fileName, apiKey string) ([]DocumentChunk, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("sarvam API key not configured")
	}

	// Serialize Sarvam requests to avoid overloading their API
	sarvamMu.Lock()
	defer sarvamMu.Unlock()

	// Retry loop for circuit breaker errors (up to 2 retries)
	const maxRetries = 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(60*attempt) * time.Second
			log.Printf("Sarvam: circuit breaker hit for %s, waiting %v before retry %d/%d", fileName, wait, attempt, maxRetries)
			time.Sleep(wait)
		}

		chunks, err := sarvamOCROnce(pdfPath, fileName, apiKey)
		if err != nil {
			if _, ok := err.(*sarvamCircuitBreakerErr); ok && attempt < maxRetries {
				continue // retry
			}
			return nil, err
		}
		return chunks, nil
	}
	return nil, fmt.Errorf("sarvam: exhausted retries for %s", fileName)
}

// sarvamOCROnce runs a single Sarvam OCR attempt.
func sarvamOCROnce(pdfPath, fileName, apiKey string) ([]DocumentChunk, error) {
	log.Printf("Sarvam Document Intelligence: starting OCR for %s", fileName)

	// Step 1: Create job
	jobID, err := sarvamCreateJob(apiKey)
	if err != nil {
		return nil, fmt.Errorf("sarvam create job: %w", err)
	}
	log.Printf("Sarvam: created job %s", jobID)

	// Step 2: Get upload URL
	uploadURL, err := sarvamGetUploadURL(apiKey, jobID, fileName)
	if err != nil {
		return nil, fmt.Errorf("sarvam get upload URL: %w", err)
	}

	// Step 3: Upload the PDF
	if err := sarvamUploadFile(uploadURL, pdfPath); err != nil {
		return nil, fmt.Errorf("sarvam upload: %w", err)
	}
	log.Printf("Sarvam: uploaded %s", fileName)

	// Step 4: Start job
	if err := sarvamStartJob(apiKey, jobID); err != nil {
		return nil, fmt.Errorf("sarvam start: %w", err)
	}
	log.Printf("Sarvam: processing started for job %s", jobID)

	// Step 5: Poll until complete (max 10 minutes per job)
	state, err := sarvamPollStatus(apiKey, jobID, 10*time.Minute)
	if err != nil {
		return nil, err // may be *sarvamCircuitBreakerErr
	}
	if state != "Completed" && state != "PartiallyCompleted" {
		return nil, fmt.Errorf("sarvam job ended with state: %s", state)
	}
	log.Printf("Sarvam: job %s completed with state=%s", jobID, state)

	// Step 6: Get download URL
	downloadURL, err := sarvamGetDownloadURL(apiKey, jobID)
	if err != nil {
		return nil, fmt.Errorf("sarvam download URL: %w", err)
	}

	// Step 7: Download and parse output
	chunks, err := sarvamDownloadAndParse(downloadURL, fileName)
	if err != nil {
		return nil, fmt.Errorf("sarvam parse output: %w", err)
	}

	log.Printf("Sarvam OCR extracted %d pages from %s", len(chunks), fileName)
	return chunks, nil
}

// --- Sarvam API helpers ---

func sarvamRequest(method, url string, body interface{}, apiKey string) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("api-subscription-key", apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	return client.Do(req)
}

func sarvamCreateJob(apiKey string) (string, error) {
	resp, err := sarvamRequest("POST", sarvamBaseURL, map[string]interface{}{
		"job_parameters": map[string]string{
			"language":      "en-IN",
			"output_format": "md",
		},
	}, apiKey)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create job failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse create job response: %w", err)
	}
	return result.JobID, nil
}

func sarvamGetUploadURL(apiKey, jobID, fileName string) (string, error) {
	resp, err := sarvamRequest("POST", sarvamBaseURL+"/upload-files", map[string]interface{}{
		"job_id": jobID,
		"files":  []string{fileName},
	}, apiKey)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("get upload URL failed (%d): %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse the response — upload_urls can be:
	// {"upload_urls": {"filename.pdf": "https://presigned-url..."}}
	// or nested: {"upload_urls": {"filename.pdf": {"url": "...", "headers": {...}}}}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return "", fmt.Errorf("parse upload response: %w (body: %s)", err, string(bodyBytes))
	}

	uploadURLsRaw, ok := raw["upload_urls"]
	if !ok {
		return "", fmt.Errorf("no upload_urls in response (body: %s)", string(bodyBytes))
	}

	// Try parsing as map[string]string first (simple case)
	var simpleMap map[string]string
	if err := json.Unmarshal(uploadURLsRaw, &simpleMap); err == nil {
		for _, url := range simpleMap {
			return url, nil
		}
	}

	// Try parsing as map[string]interface{} (nested object case)
	var nestedMap map[string]interface{}
	if err := json.Unmarshal(uploadURLsRaw, &nestedMap); err == nil {
		for _, val := range nestedMap {
			switch v := val.(type) {
			case string:
				return v, nil
			case map[string]interface{}:
				// Look for "url" key in nested object
				if urlStr, ok := v["url"].(string); ok {
					return urlStr, nil
				}
				// Try first string-valued key
				for _, inner := range v {
					if s, ok := inner.(string); ok && strings.HasPrefix(s, "http") {
						return s, nil
					}
				}
			}
		}
	}

	log.Printf("Sarvam upload_urls raw response: %s", string(bodyBytes))
	return "", fmt.Errorf("could not extract upload URL from response")
}

func sarvamUploadFile(uploadURL, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "application/pdf")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func sarvamStartJob(apiKey, jobID string) error {
	url := fmt.Sprintf("%s/%s/start", sarvamBaseURL, jobID)
	resp, err := sarvamRequest("POST", url, nil, apiKey)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("start job failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func sarvamPollStatus(apiKey, jobID string, timeout time.Duration) (string, error) {
	url := fmt.Sprintf("%s/%s/status", sarvamBaseURL, jobID)
	deadline := time.Now().Add(timeout)
	pollInterval := 3 * time.Second

	for time.Now().Before(deadline) {
		resp, err := sarvamRequest("GET", url, nil, apiKey)
		if err != nil {
			return "", err
		}

		// Read the full response body for better error reporting
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var status struct {
			JobState     string `json:"job_state"`
			ErrorMessage string `json:"error_message"`
			JobDetails   []struct {
				State        string `json:"state"`
				ErrorMessage string `json:"error_message"`
				ErrorCode    string `json:"error_code"`
			} `json:"job_details"`
		}
		if err := json.Unmarshal(bodyBytes, &status); err != nil {
			log.Printf("Sarvam: failed to parse status response: %v (body: %s)", err, string(bodyBytes))
			return "", fmt.Errorf("parse status: %w", err)
		}

		switch status.JobState {
		case "Completed", "PartiallyCompleted":
			return status.JobState, nil
		case "Failed":
			// Try to extract per-file error details
			errMsg := status.ErrorMessage
			isCircuitBreaker := false
			for _, detail := range status.JobDetails {
				if detail.ErrorMessage != "" {
					errMsg = detail.ErrorMessage
				}
				if strings.Contains(detail.ErrorMessage, "CIRCUIT_BREAKER") ||
					strings.Contains(detail.ErrorCode, "CIRCUIT_BREAKER") {
					isCircuitBreaker = true
				}
			}
			if errMsg == "" {
				errMsg = "unknown error (check Sarvam dashboard)"
			}
			log.Printf("Sarvam: job %s failed, raw response: %s", jobID, string(bodyBytes))
			if isCircuitBreaker {
				return status.JobState, &sarvamCircuitBreakerErr{msg: errMsg}
			}
			return status.JobState, fmt.Errorf("job failed: %s", errMsg)
		}

		// Still running — wait and poll again
		time.Sleep(pollInterval)
		// Increase interval slightly for long jobs
		if pollInterval < 10*time.Second {
			pollInterval += time.Second
		}
	}

	return "", fmt.Errorf("timeout waiting for job completion")
}

func sarvamGetDownloadURL(apiKey, jobID string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/download-files", sarvamBaseURL, jobID)
	resp, err := sarvamRequest("POST", apiURL, nil, apiKey)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("get download URL failed (%d): %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response — download_urls may be flat or nested like upload_urls
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return "", fmt.Errorf("parse download response: %w (body: %s)", err, string(bodyBytes))
	}

	downloadURLsRaw, ok := raw["download_urls"]
	if !ok {
		return "", fmt.Errorf("no download_urls in response (body: %s)", string(bodyBytes))
	}

	// Try simple map[string]string
	var simpleMap map[string]string
	if err := json.Unmarshal(downloadURLsRaw, &simpleMap); err == nil {
		for _, u := range simpleMap {
			return u, nil
		}
	}

	// Try nested map[string]interface{}
	var nestedMap map[string]interface{}
	if err := json.Unmarshal(downloadURLsRaw, &nestedMap); err == nil {
		for _, val := range nestedMap {
			switch v := val.(type) {
			case string:
				return v, nil
			case map[string]interface{}:
				if urlStr, ok := v["url"].(string); ok {
					return urlStr, nil
				}
				for _, inner := range v {
					if s, ok := inner.(string); ok && strings.HasPrefix(s, "http") {
						return s, nil
					}
				}
			}
		}
	}

	log.Printf("Sarvam download_urls raw response: %s", string(bodyBytes))
	return "", fmt.Errorf("could not extract download URL from response")
}

func sarvamDownloadAndParse(downloadURL, fileName string) ([]DocumentChunk, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed (%d): %s", resp.StatusCode, string(body))
	}

	// Output is a ZIP file — download to temp file first
	tmpFile, err := os.CreateTemp("", "sarvam-output-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	_, err = io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		return nil, fmt.Errorf("download to temp: %w", err)
	}

	// Open ZIP and extract content
	zipReader, err := zip.OpenReader(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zipReader.Close()

	// Collect all text files from the ZIP
	var allTexts []struct {
		name string
		text string
	}

	for _, f := range zipReader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext != ".md" && ext != ".txt" && ext != ".html" {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			log.Printf("Sarvam: failed to open %s in zip: %v", f.Name, err)
			continue
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}

		text := strings.TrimSpace(string(content))
		if len(text) > 20 {
			allTexts = append(allTexts, struct {
				name string
				text string
			}{name: f.Name, text: text})
		}
	}

	log.Printf("Sarvam: ZIP contains %d text file(s) for %s", len(allTexts), fileName)

	if len(allTexts) == 0 {
		return nil, fmt.Errorf("sarvam output contained no extractable text")
	}

	var chunks []DocumentChunk

	if len(allTexts) > 1 {
		// Multiple files in ZIP — treat each as a page
		for i, t := range allTexts {
			pageNum := i + 1
			if num := extractPageNum(t.name); num > 0 {
				pageNum = num
			}
			chunks = append(chunks, DocumentChunk{
				PageNumber: pageNum,
				Document:   fileName,
				Text:       stripMarkdownFormatting(t.text),
			})
		}
	} else {
		// Single merged file — split into pages by separators
		// Sarvam outputs page breaks as "---" (horizontal rules) or "<!-- page X -->" markers
		chunks = splitMergedMarkdownIntoPages(allTexts[0].text, fileName)
	}

	if len(chunks) == 0 {
		return nil, fmt.Errorf("sarvam output contained no extractable text after splitting")
	}

	return chunks, nil
}

// splitMergedMarkdownIntoPages splits a single merged markdown document into pages.
// It tries multiple separator strategies:
// 1. Horizontal rules (---) that act as page breaks
// 2. Page header patterns (## Page X, # Page X)
// 3. Form feed characters (\f)
// 4. Falls back to splitting by word count if no separators found
func splitMergedMarkdownIntoPages(text, fileName string) []DocumentChunk {
	text = stripMarkdownFormatting(text)

	var sections []string

	// Try splitting by horizontal rules (common Sarvam separator)
	hrSplit := regexp.MustCompile(`\n-{3,}\n`)
	parts := hrSplit.Split(text, -1)
	if len(parts) > 1 {
		sections = parts
	}

	// Try form feed characters
	if len(sections) <= 1 {
		ffParts := strings.Split(text, "\f")
		if len(ffParts) > 1 {
			sections = ffParts
		}
	}

	// Try page header patterns
	if len(sections) <= 1 {
		pageHeaderRe := regexp.MustCompile(`(?m)^#{1,2}\s+[Pp]age\s+\d+`)
		indices := pageHeaderRe.FindAllStringIndex(text, -1)
		if len(indices) > 1 {
			sections = nil
			for i, idx := range indices {
				var end int
				if i+1 < len(indices) {
					end = indices[i+1][0]
				} else {
					end = len(text)
				}
				sections = append(sections, text[idx[0]:end])
			}
		}
	}

	// Fallback: split into ~500 word chunks (synthetic pages)
	if len(sections) <= 1 {
		words := strings.Fields(text)
		wordsPerPage := 500
		for i := 0; i < len(words); i += wordsPerPage {
			end := i + wordsPerPage
			if end > len(words) {
				end = len(words)
			}
			sections = append(sections, strings.Join(words[i:end], " "))
		}
	}

	var chunks []DocumentChunk
	for i, section := range sections {
		cleaned := strings.TrimSpace(section)
		if len(cleaned) > 20 {
			chunks = append(chunks, DocumentChunk{
				PageNumber: i + 1,
				Document:   fileName,
				Text:       cleaned,
			})
		}
	}

	log.Printf("Sarvam: split merged output into %d pages for %s", len(chunks), fileName)
	return chunks
}

// extractPageNum tries to extract a page number from a filename like "page_001.md" or "1.md"
func extractPageNum(name string) int {
	base := filepath.Base(name)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	re := regexp.MustCompile(`(\d+)`)
	m := re.FindString(base)
	if m != "" {
		n, _ := strconv.Atoi(m)
		return n
	}
	return 0
}

// stripMarkdownFormatting removes basic markdown formatting to get clean text
// while preserving the content structure for chunking.
func stripMarkdownFormatting(text string) string {
	// Remove markdown headers (# ## ### etc.)
	re := regexp.MustCompile(`(?m)^#{1,6}\s+`)
	text = re.ReplaceAllString(text, "")

	// Remove bold/italic markers
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "__", "")
	text = strings.ReplaceAll(text, "*", "")
	text = strings.ReplaceAll(text, "_", " ")

	// Remove markdown links [text](url) → text
	linkRe := regexp.MustCompile(`\[([^\]]+)\]\([^\)]+\)`)
	text = linkRe.ReplaceAllString(text, "$1")

	// Remove excessive newlines
	nlRe := regexp.MustCompile(`\n{3,}`)
	text = nlRe.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
