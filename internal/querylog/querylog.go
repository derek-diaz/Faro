package querylog

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
)

var logPattern = regexp.MustCompile(`\s(\d+\.\d+\.\d+\.\d+|\[[0-9a-fA-F:]+\]|[0-9a-fA-F:]+):\d+\s+-\s+\d+\s+"([A-Z]+)\s+IN\s+([^"\s]+).*"\s+([A-Z]+).*\s([0-9.]+)s`)

type logEntry struct {
	clientIP  string
	queryType string
	domain    string
	rcode     string
	latencyMS float64
	upstream  string
	observed  bool
}

type Tailer struct {
	Store *db.Store
	Path  string
}

type logCursor struct {
	Identity string `json:"identity"`
	Offset   int64  `json:"offset"`
}

func NewTailer(store *db.Store, path string) *Tailer {
	return &Tailer{Store: store, Path: path}
}

func (t *Tailer) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	cursor, loaded := loadCursor(t.Path + ".cursor")
	if !loaded {
		cursor = cursorAtEnd(t.Path)
		_ = saveCursor(t.Path+".cursor", cursor)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := t.readAvailable(ctx, cursor)
			if err != nil {
				if !os.IsNotExist(err) {
					log.Printf("query log read failed: %v", err)
				}
				continue
			}
			cursor = next
			if err := saveCursor(t.Path+".cursor", cursor); err != nil {
				log.Printf("query log cursor save failed: %v", err)
			}
		}
	}
}

func cursorAtEnd(path string) logCursor {
	file, err := os.Open(path)
	if err != nil {
		return logCursor{}
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return logCursor{}
	}
	return logCursor{Identity: fileIdentity(file), Offset: stat.Size()}
}

func (t *Tailer) readAvailable(ctx context.Context, cursor logCursor) (logCursor, error) {
	file, err := os.Open(t.Path)
	if err != nil {
		return cursor, err
	}
	defer file.Close()

	currentInfo, err := file.Stat()
	if err != nil {
		return cursor, err
	}

	currentIdentity := fileIdentity(file)
	if cursor.Identity == "" || cursor.Identity == currentIdentity {
		offset := cursor.Offset
		if currentInfo.Size() < offset {
			offset = 0
		}
		position, err := t.readOpenFile(ctx, file, offset)
		if err != nil {
			return cursor, err
		}
		return logCursor{Identity: currentIdentity, Offset: position}, nil
	}

	rotatedIndex := findRotatedIndex(t.Path, cursor.Identity)
	if rotatedIndex > 0 {
		if _, err := t.readPath(ctx, rotatedPath(t.Path, rotatedIndex), cursor.Offset); err != nil {
			return cursor, err
		}
		for index := rotatedIndex - 1; index >= 1; index-- {
			if _, err := t.readPath(ctx, rotatedPath(t.Path, index), 0); err != nil {
				return cursor, err
			}
		}
	} else {
		log.Printf("query log rotated beyond retained backups; some raw entries may have been skipped")
	}

	position, err := t.readOpenFile(ctx, file, 0)
	if err != nil {
		return cursor, err
	}
	return logCursor{Identity: currentIdentity, Offset: position}, nil
}

func (t *Tailer) readPath(ctx context.Context, path string, offset int64) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer file.Close()
	return t.readOpenFile(ctx, file, offset)
}

func (t *Tailer) readOpenFile(ctx context.Context, file *os.File, offset int64) (int64, error) {
	if _, err := file.Seek(offset, 0); err != nil {
		return offset, err
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		t.insert(ctx, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return offset, err
	}
	position, err := file.Seek(0, 1)
	if err != nil {
		return offset, err
	}
	return position, nil
}

func findRotatedIndex(path, identity string) int {
	matches, err := filepath.Glob(path + ".*")
	if err != nil {
		return 0
	}
	for _, match := range matches {
		index, err := strconv.Atoi(strings.TrimPrefix(match, path+"."))
		if err != nil || index < 1 {
			continue
		}
		candidate, err := os.Open(match)
		if err == nil && fileIdentity(candidate) == identity {
			_ = candidate.Close()
			return index
		}
		if candidate != nil {
			_ = candidate.Close()
		}
	}
	return 0
}

func fileIdentity(file *os.File) string {
	if file == nil {
		return ""
	}
	position, _ := file.Seek(0, 1)
	_, _ = file.Seek(0, 0)
	reader := bufio.NewReader(io.LimitReader(file, 4096))
	firstLine, _ := reader.ReadBytes('\n')
	_, _ = file.Seek(position, 0)
	if len(firstLine) == 0 {
		return "empty"
	}
	sum := sha256.Sum256(firstLine)
	return fmt.Sprintf("%x", sum[:])
}

func loadCursor(path string) (logCursor, bool) {
	input, err := os.Open(path)
	if err != nil {
		return logCursor{}, false
	}
	defer input.Close()
	var cursor logCursor
	if json.NewDecoder(io.LimitReader(input, 4096)).Decode(&cursor) != nil || cursor.Offset < 0 {
		return logCursor{}, false
	}
	return cursor, true
}

func saveCursor(path string, cursor logCursor) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".query-cursor-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := json.NewEncoder(temp).Encode(cursor); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tempName, path)
}

func rotatedPath(path string, index int) string {
	return path + "." + strconv.Itoa(index)
}

func (t *Tailer) insert(ctx context.Context, line string) {
	entry, ok := parseLine(line)
	if !ok {
		return
	}

	decision := coredns.ExplainDomainForClient(ctx, t.Store, entry.domain, entry.clientIP)
	action := decision.Action
	source := ""
	if action == "blocked" {
		if decision.ManualBlock != nil {
			source = "manual"
		} else {
			source = "blocklist"
		}
	} else if isLocalRecord(ctx, t.Store, entry.domain) {
		source = "local"
	} else if !entry.observed || entry.upstream != "" {
		source = "upstream"
	} else {
		source = "cache"
	}
	decision.Upstream = entry.upstream
	decision.ResponseCode = entry.rcode
	decision.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	decision.Confidence = decisionConfidence(source, entry.upstream)
	decision.Reason = decisionReason(decision, source, entry.upstream)
	metadata, err := json.Marshal(decision)
	if err != nil {
		metadata = []byte("{}")
	}

	_, err = t.Store.DB.ExecContext(ctx, `
		INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, time.Now().UTC().Format(time.RFC3339), entry.clientIP, entry.domain, entry.queryType, action, source, entry.upstream, entry.latencyMS, entry.rcode, decision.Reason, string(metadata))
	if err != nil {
		log.Printf("insert dns query failed: %v", err)
	}
}

func decisionConfidence(source, upstream string) string {
	if source == "upstream" && upstream != "" {
		return "observed"
	}
	if source == "cache" {
		return "inferred"
	}
	return "configuration_snapshot"
}

func decisionReason(decision coredns.DomainDecision, source, upstream string) string {
	if decision.Action == "blocked" || decision.LocalRecord != nil {
		if decision.LocalRecord != nil && decision.Action != "blocked" {
			return "Matched a Faro Local DNS " + decision.LocalRecord.Type + " record pointing to " + decision.LocalRecord.Value + "."
		}
		return decision.Reason
	}
	resolution := "Forwarded to a configured upstream resolver."
	if source == "cache" {
		resolution = "Answered from Faro's DNS cache without contacting an upstream."
	} else if upstream != "" {
		resolution = "Forwarded to upstream resolver " + upstream + "."
	}
	if decision.Allowlist != nil {
		return decision.Reason + " " + resolution
	}
	return resolution
}

func parseLine(line string) (logEntry, bool) {
	if marker := strings.Index(line, "FARO|"); marker >= 0 {
		parts := strings.Split(strings.TrimSpace(line[marker:]), "|")
		if len(parts) < 7 {
			return logEntry{}, false
		}
		latency, err := time.ParseDuration(parts[5])
		if err != nil {
			return logEntry{}, false
		}
		return logEntry{
			clientIP:  strings.Trim(parts[1], "[]"),
			queryType: strings.ToUpper(strings.TrimSpace(parts[2])),
			domain:    strings.TrimSuffix(strings.ToLower(strings.TrimSpace(parts[3])), "."),
			rcode:     strings.ToUpper(strings.TrimSpace(parts[4])),
			latencyMS: float64(latency.Microseconds()) / 1000,
			upstream:  normalizeUpstream(parts[6]),
			observed:  true,
		}, true
	}

	match := logPattern.FindStringSubmatch(line)
	if len(match) == 0 {
		return logEntry{}, false
	}
	latencySeconds, _ := strconv.ParseFloat(match[5], 64)
	return logEntry{
		clientIP:  strings.Trim(match[1], "[]"),
		queryType: match[2],
		domain:    strings.TrimSuffix(strings.ToLower(match[3]), "."),
		rcode:     match[4],
		latencyMS: latencySeconds * 1000,
	}, true
}

func normalizeUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return ""
	}
	for _, prefix := range []string{"udp://", "tcp://", "tls://"} {
		value = strings.TrimPrefix(value, prefix)
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func isLocalRecord(ctx context.Context, store *db.Store, domain string) bool {
	var exists int
	err := store.DB.QueryRowContext(ctx, `SELECT 1 FROM dns_records WHERE hostname = ? AND type IN ('A', 'AAAA') LIMIT 1`, domain).Scan(&exists)
	return err == nil
}
