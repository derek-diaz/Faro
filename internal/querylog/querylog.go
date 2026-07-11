package querylog

import (
	"bufio"
	"context"
	"log"
	"net"
	"os"
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

func NewTailer(store *db.Store, path string) *Tailer {
	return &Tailer{Store: store, Path: path}
}

func (t *Tailer) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	offset := currentSize(t.Path)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := t.readFrom(ctx, offset)
			if err != nil {
				if !os.IsNotExist(err) {
					log.Printf("query log read failed: %v", err)
				}
				continue
			}
			offset = next
		}
	}
}

func currentSize(path string) int64 {
	stat, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return stat.Size()
}

func (t *Tailer) readFrom(ctx context.Context, offset int64) (int64, error) {
	file, err := os.Open(t.Path)
	if err != nil {
		return offset, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return offset, err
	}
	if stat.Size() < offset {
		offset = 0
	}
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

func (t *Tailer) insert(ctx context.Context, line string) {
	entry, ok := parseLine(line)
	if !ok {
		return
	}

	blocked, source := coredns.IsBlocked(ctx, t.Store, entry.domain)
	action := "allowed"
	if blocked {
		action = "blocked"
		source = "blocklist"
	} else if isLocalRecord(ctx, t.Store, entry.domain) {
		source = "local"
	} else if !entry.observed || entry.upstream != "" {
		source = "upstream"
	} else {
		source = "cache"
	}

	_, err := t.Store.DB.ExecContext(ctx, `INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), entry.clientIP, entry.domain, entry.queryType, action, source, entry.upstream, entry.latencyMS)
	if err != nil {
		log.Printf("insert dns query failed: %v", err)
	}
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
