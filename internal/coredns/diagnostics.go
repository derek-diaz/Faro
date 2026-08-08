package coredns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const maxDiagnosticContentBytes = 1 << 20

// DiagnosticFile is a bounded, read-only view of one accepted or generated
// CoreDNS file. Hashes cover the complete file even when the displayed content
// is truncated for a large blocklist.
type DiagnosticFile struct {
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	Active             string `json:"active"`
	Generated          string `json:"generated"`
	ActiveHash         string `json:"active_hash,omitempty"`
	GeneratedHash      string `json:"generated_hash,omitempty"`
	ActiveBytes        int64  `json:"active_bytes"`
	GeneratedBytes     int64  `json:"generated_bytes"`
	ActiveTruncated    bool   `json:"active_truncated,omitempty"`
	GeneratedTruncated bool   `json:"generated_truncated,omitempty"`
	Referenced         bool   `json:"referenced"`
	Matches            bool   `json:"matches"`
}

// Diagnostics describes the active CoreDNS files and the candidate Faro
// would render from the current control-plane state. It intentionally omits
// the absolute config path and offers no mutation mechanism.
type Diagnostics struct {
	Status                string           `json:"status"`
	GeneratedAt           string           `json:"generated_at"`
	Bootstrapped          bool             `json:"bootstrapped"`
	ActiveCorefileHash    string           `json:"active_corefile_hash,omitempty"`
	GeneratedCorefileHash string           `json:"generated_corefile_hash,omitempty"`
	ReloadsTotal          uint64           `json:"reloads_total"`
	ReloadFailures        uint64           `json:"reload_failures_total"`
	Files                 []DiagnosticFile `json:"files"`
	Error                 string           `json:"error,omitempty"`
}

type diagnosticSnapshot struct {
	content   string
	hash      string
	bytes     int64
	truncated bool
}

func filesFromRenderedState(state renderedFiles) map[string][]byte {
	files := map[string][]byte{
		"Corefile":        []byte(state.Corefile),
		"faro.hosts":      []byte(state.LocalHosts + "\n" + state.BlockHosts),
		"local.hosts":     []byte(state.LocalHosts),
		"blocklist.hosts": []byte(state.BlockHosts),
	}
	for name, content := range state.ProtectionHosts {
		files[name] = []byte(content)
	}
	return files
}

func runtimeFilesFromRenderedState(state renderedFiles) (map[string][]byte, error) {
	allFiles := filesFromRenderedState(state)
	hostFiles, err := corefileHostFiles(state.Corefile)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"Corefile": allFiles["Corefile"]}
	for _, name := range hostFiles {
		content, ok := allFiles[name]
		if !ok {
			return nil, fmt.Errorf("generated Corefile references missing hosts file %q", name)
		}
		files[name] = content
	}
	return files, nil
}

// Diagnostics serializes with Apply so the active files and the generated
// candidate cannot be observed halfway through a replacement.
func (manager *Manager) Diagnostics(ctx context.Context) (Diagnostics, error) {
	manager.applyMu.Lock()
	defer manager.applyMu.Unlock()

	reloads, reloadFailures := ReloadTotals()
	result := Diagnostics{
		Status:         "unavailable",
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Bootstrapped:   manager.bootstrapped,
		ReloadsTotal:   reloads,
		ReloadFailures: reloadFailures,
	}

	active, err := readActiveDiagnosticFiles(manager.ConfigDir)
	if err != nil {
		return result, fmt.Errorf("read active CoreDNS files: %w", err)
	}

	generated := map[string]diagnosticSnapshot{}
	state, renderErr := manager.render(ctx)
	if renderErr == nil {
		for name, content := range filesFromRenderedState(state) {
			generated[name] = snapshotDiagnosticBytes(content)
		}
	} else {
		result.Status = "generator_error"
		result.Error = renderErr.Error()
	}

	activeReferences, err := activeCorefileReferences(active)
	if err != nil {
		return result, fmt.Errorf("read active CoreDNS references: %w", err)
	}
	var generatedReferences []string
	if renderErr == nil {
		generatedReferences, err = corefileHostFiles(generated["Corefile"].content)
		if err != nil {
			return result, fmt.Errorf("read generated CoreDNS references: %w", err)
		}
	}
	referenced := make(map[string]struct{}, len(activeReferences)+len(generatedReferences)+1)
	referenced["Corefile"] = struct{}{}
	for _, name := range activeReferences {
		referenced[name] = struct{}{}
	}
	for _, name := range generatedReferences {
		referenced[name] = struct{}{}
	}

	names := make(map[string]struct{}, len(active)+len(generated))
	for name := range active {
		names[name] = struct{}{}
	}
	for name := range generated {
		names[name] = struct{}{}
	}
	sortedNames := make([]string, 0, len(names))
	for name := range names {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	allMatch := renderErr == nil && len(referenced) > 0
	for _, name := range sortedNames {
		activeFile, activeOK := active[name]
		generatedFile, generatedOK := generated[name]
		matches := activeOK && generatedOK && activeFile.hash == generatedFile.hash
		isReferenced := false
		if _, ok := referenced[name]; ok {
			isReferenced = true
		}
		if isReferenced && !matches {
			allMatch = false
		}
		result.Files = append(result.Files, DiagnosticFile{
			Name:               name,
			Kind:               diagnosticFileKind(name),
			Active:             activeFile.content,
			Generated:          generatedFile.content,
			ActiveHash:         activeFile.hash,
			GeneratedHash:      generatedFile.hash,
			ActiveBytes:        activeFile.bytes,
			GeneratedBytes:     generatedFile.bytes,
			ActiveTruncated:    activeFile.truncated,
			GeneratedTruncated: generatedFile.truncated,
			Referenced:         isReferenced,
			Matches:            matches,
		})
	}

	if renderErr == nil {
		switch {
		case allMatch:
			result.Status = "healthy"
		case activeCorefileMissing(active):
			result.Status = "not_initialized"
		default:
			result.Status = "drifted"
		}
	}
	if activeFile, ok := active["Corefile"]; ok {
		result.ActiveCorefileHash = activeFile.hash
	}
	if generatedFile, ok := generated["Corefile"]; ok {
		result.GeneratedCorefileHash = generatedFile.hash
	}
	return result, nil
}

func activeCorefileMissing(active map[string]diagnosticSnapshot) bool {
	_, ok := active["Corefile"]
	return !ok
}

func activeCorefileReferences(active map[string]diagnosticSnapshot) ([]string, error) {
	corefile, ok := active["Corefile"]
	if !ok {
		return nil, nil
	}
	return corefileHostFiles(corefile.content)
}

func diagnosticFileKind(name string) string {
	if name == "Corefile" {
		return "corefile"
	}
	return "hosts"
}

func readActiveDiagnosticFiles(dir string) (map[string]diagnosticSnapshot, error) {
	result := map[string]diagnosticSnapshot{}
	corefile, err := readDiagnosticFile(filepath.Join(dir, "Corefile"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result["Corefile"] = corefile
	hostFiles, err := corefileHostFiles(corefile.content)
	if err != nil {
		return nil, err
	}
	for _, name := range hostFiles {
		snapshot, err := readDiagnosticFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[name] = snapshot
	}
	return result, nil
}

func readDiagnosticFile(path string) (diagnosticSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return diagnosticSnapshot{}, err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	preview := make([]byte, 0, maxDiagnosticContentBytes)
	buffer := make([]byte, 32*1024)
	var total int64
	truncated := false
	for {
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
			total += int64(read)
			remaining := maxDiagnosticContentBytes - len(preview)
			if remaining > 0 {
				count := minInt(remaining, read)
				preview = append(preview, buffer[:count]...)
				if count < read {
					truncated = true
				}
			} else {
				truncated = true
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return diagnosticSnapshot{}, readErr
		}
	}
	return diagnosticSnapshot{content: string(preview), hash: hex.EncodeToString(hash.Sum(nil)), bytes: total, truncated: truncated}, nil
}

func snapshotDiagnosticBytes(content []byte) diagnosticSnapshot {
	sum := sha256.Sum256(content)
	preview := content
	truncated := false
	if len(preview) > maxDiagnosticContentBytes {
		preview = preview[:maxDiagnosticContentBytes]
		truncated = true
	}
	return diagnosticSnapshot{
		content:   string(preview),
		hash:      hex.EncodeToString(sum[:]),
		bytes:     int64(len(content)),
		truncated: truncated,
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
