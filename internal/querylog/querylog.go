package querylog

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
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
	deviceidentity "github.com/derek/faro/internal/devices"
)

const (
	cursorSuffix         = ".cursor"
	queryIngestBatchSize = 256
)

var logPattern = regexp.MustCompile(`\s(\d+\.\d+\.\d+\.\d+|\[[0-9a-fA-F:]+]|[0-9a-fA-F:]+):\d+\s+-\s+\d+\s+"([A-Z]+)\s+IN\s+([^"\s]+).*"\s+([A-Z]+).*\s([0-9.]+)s`)

var errStoragePaused = errors.New("query history storage is waiting to retry")

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

func (tailer *Tailer) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	cursor, loaded := loadCursor(tailer.Path + cursorSuffix)
	if !loaded {
		cursor = cursorAtEnd(tailer.Path)
		_ = saveCursor(tailer.Path+cursorSuffix, cursor)
	}
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = tailer.readAvailable(flushCtx, cursor)
			cancel()
			return
		case <-ticker.C:
			next, err := tailer.readAvailable(ctx, cursor)
			cursor = next
			if err != nil {
				if !os.IsNotExist(err) && !errors.Is(err, errStoragePaused) && ctx.Err() == nil {
					log.Printf("query log read failed: %v", err)
				}
				continue
			}
			cursor = next
			if err := saveCursor(tailer.Path+cursorSuffix, cursor); err != nil {
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
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("query log cursor file close failed: %v", err)
		}
	}()
	stat, err := file.Stat()
	if err != nil {
		return logCursor{}
	}
	return logCursor{Identity: fileIdentity(file), Offset: stat.Size()}
}

func (tailer *Tailer) readAvailable(ctx context.Context, cursor logCursor) (next logCursor, err error) {
	// The database checkpoint commits with the queries. It takes precedence
	// over the legacy sidecar, including after a crash before sidecar saving.
	var saved logCursor
	if err := tailer.Store.DB.QueryRowContext(ctx, `SELECT identity, offset FROM query_log_progress WHERE path = ?`, tailer.Path).Scan(&saved.Identity, &saved.Offset); err == nil {
		cursor = saved
	} else if !errors.Is(err, sql.ErrNoRows) {
		return cursor, err
	}
	next = cursor
	file, err := os.Open(tailer.Path)
	if err != nil {
		return cursor, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

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
		position, err := tailer.readOpenFile(ctx, file, offset)
		return logCursor{Identity: currentIdentity, Offset: position}, err
	}

	rotatedIndex := findRotatedIndex(tailer.Path, cursor.Identity)
	if rotatedIndex > 0 {
		position, err := tailer.readPath(ctx, rotatedPath(tailer.Path, rotatedIndex), cursor.Offset)
		next = logCursor{Identity: cursor.Identity, Offset: position}
		if err != nil {
			return next, err
		}
		for index := rotatedIndex - 1; index >= 1; index-- {
			if _, err := tailer.readPath(ctx, rotatedPath(tailer.Path, index), 0); err != nil {
				return next, err
			}
		}
	} else if cursor.Offset > 0 {
		log.Printf("query log rotated beyond retained backups; some raw entries may have been skipped")
	}

	position, err := tailer.readOpenFile(ctx, file, 0)
	return logCursor{Identity: currentIdentity, Offset: position}, err
}

func (tailer *Tailer) readPath(ctx context.Context, path string, offset int64) (position int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	return tailer.readOpenFile(ctx, file, offset)
}

func (tailer *Tailer) readOpenFile(ctx context.Context, file *os.File, offset int64) (int64, error) {
	stat, err := file.Stat()
	if err != nil {
		return offset, err
	}
	// Read a finite snapshot, leave partial final lines for the next poll,
	// and cap memory even when a malformed line has no newline.
	reader := bufio.NewReaderSize(io.NewSectionReader(file, offset, max(0, stat.Size()-offset)), 64<<10)
	identity := fileIdentity(file)
	position, committed := offset, offset
	entries := make([]logEntry, 0, queryIngestBatchSize)
	complete, lines := offset, 0
	oversized := false
	flush := func() error {
		if complete == committed {
			return nil
		}
		if err := tailer.insertBatch(ctx, entries, logCursor{Identity: identity, Offset: complete}); err != nil {
			return err
		}
		committed = complete
		entries = entries[:0]
		lines = 0
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return committed, err
		}
		line, readErr := reader.ReadSlice('\n')
		position += int64(len(line))
		if errors.Is(readErr, bufio.ErrBufferFull) {
			oversized = true
			continue
		}
		if readErr != nil {
			if err := flush(); err != nil {
				return committed, err
			}
			if errors.Is(readErr, io.EOF) {
				return committed, nil
			}
			return committed, readErr
		}
		if !oversized {
			if entry, ok := parseLine(string(line)); ok && net.ParseIP(entry.clientIP) != nil {
				entries = append(entries, entry)
			}
		}
		oversized = false
		complete = position
		lines++
		if lines == queryIngestBatchSize {
			if err := flush(); err != nil {
				return committed, err
			}
		}
	}
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
	defer func() {
		if err := input.Close(); err != nil {
			log.Printf("query log cursor close failed: %v", err)
		}
	}()
	var cursor logCursor
	if json.NewDecoder(io.LimitReader(input, 4096)).Decode(&cursor) != nil || cursor.Offset < 0 {
		return logCursor{}, false
	}
	return cursor, true
}

func saveCursor(path string, cursor logCursor) (err error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".query-cursor-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() {
		if removeErr := os.Remove(tempName); removeErr != nil && !os.IsNotExist(removeErr) {
			err = errors.Join(err, removeErr)
		}
	}()
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

func (tailer *Tailer) insert(ctx context.Context, line string) {
	entry, ok := parseLine(line)
	if !ok {
		return
	}
	tailer.insertBatch(ctx, []logEntry{entry})
}

func (tailer *Tailer) insertParsed(ctx context.Context, entry logEntry) {
	tailer.insertBatch(ctx, []logEntry{entry})
}

type pendingQuery struct {
	timestamp        string
	clientIP         string
	deviceID         int64
	domain           string
	queryType        string
	action           string
	source           string
	upstream         string
	latencyMS        float64
	rcode            string
	decisionReason   string
	decisionMetadata string
}

func (tailer *Tailer) insertBatch(ctx context.Context, entries []logEntry, checkpoint ...logCursor) (err error) {
	if len(entries) == 0 && len(checkpoint) == 0 {
		return nil
	}
	if !tailer.Store.ActivityStorageWriteAllowed() {
		return errStoragePaused
	}
	defer func() {
		if err != nil && ctx.Err() == nil && !errors.Is(err, errStoragePaused) {
			tailer.Store.ReportActivityWriteFailure(err)
		}
	}()
	// Scope identity reuse to one batch so address reassignment and device
	// merges are observed on the next poll without a long-lived stale cache.
	identities := make(map[string]int64)
	queries := make([]pendingQuery, 0, len(entries))
	for _, entry := range entries {
		deviceID, known := identities[entry.clientIP]
		if !known {
			deviceID, err = deviceidentity.ResolveAddress(ctx, tailer.Store, entry.clientIP, "dns")
			if err != nil {
				return fmt.Errorf("resolve DNS client identity: %w", err)
			}
			identities[entry.clientIP] = deviceID
		}
		queries = append(queries, tailer.prepareEntry(ctx, entry, deviceID))
	}
	tx, err := tailer.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata)
		VALUES(?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, query := range queries {
		if _, err := stmt.ExecContext(ctx,
			query.timestamp, query.clientIP, query.deviceID, query.domain, query.queryType,
			query.action, query.source, query.upstream, query.latencyMS, query.rcode,
			query.decisionReason, query.decisionMetadata,
		); err != nil {
			return err
		}
	}
	if len(checkpoint) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO query_log_progress(path, identity, offset) VALUES(?, ?, ?)
   ON CONFLICT(path) DO UPDATE SET identity = excluded.identity, offset = excluded.offset`,
			tailer.Path, checkpoint[0].Identity, checkpoint[0].Offset); err != nil {
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tailer.Store.ReportActivityWriteSuccess()
	return nil
}

func (tailer *Tailer) prepareEntry(ctx context.Context, entry logEntry, deviceID int64) pendingQuery {
	decision := coredns.ExplainDomainForClient(ctx, tailer.Store, entry.domain, entry.clientIP)
	action := decision.Action
	source := sourceForEntry(ctx, tailer.Store, entry, decision)
	decision.Upstream = entry.upstream
	decision.ResponseCode = entry.rcode
	decision.CapturedAt = time.Now().UTC().Format(time.RFC3339)
	decision.Confidence = decisionConfidence(source, entry.upstream)
	decision.Reason = decisionReason(decision, source, entry.upstream)
	metadata, err := json.Marshal(decision)
	if err != nil {
		metadata = []byte("{}")
	}
	return pendingQuery{
		timestamp:        time.Now().UTC().Format(time.RFC3339),
		clientIP:         entry.clientIP,
		deviceID:         deviceID,
		domain:           entry.domain,
		queryType:        entry.queryType,
		action:           action,
		source:           source,
		upstream:         entry.upstream,
		latencyMS:        entry.latencyMS,
		rcode:            entry.rcode,
		decisionReason:   decision.Reason,
		decisionMetadata: string(metadata),
	}
}

func sourceForEntry(_ context.Context, _ *db.Store, entry logEntry, decision coredns.DomainDecision) string {
	if decision.Action == "blocked" {
		if decision.ManualBlock != nil {
			return "manual"
		}
		return "blocklist"
	}
	if decision.LocalRecord != nil && (decision.LocalRecord.Type == "A" || decision.LocalRecord.Type == "AAAA") {
		return "local"
	}
	if !entry.observed || entry.upstream != "" {
		return "upstream"
	}
	return "cache"
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
	} else if upstream == "doh" {
		resolution = "Forwarded through Faro's encrypted DNS-over-HTTPS connection."
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
	latencySeconds, err := strconv.ParseFloat(match[5], 64)
	if err != nil {
		return logEntry{}, false
	}
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
	if host, port, err := net.SplitHostPort(value); err == nil {
		host = strings.Trim(host, "[]")
		if port == "5053" && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
			return "doh"
		}
		return host
	}
	return strings.Trim(value, "[]")
}
