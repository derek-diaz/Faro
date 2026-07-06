package querylog

import (
	"bufio"
	"context"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/derek/faro/internal/coredns"
	"github.com/derek/faro/internal/db"
)

var logPattern = regexp.MustCompile(`\s(\d+\.\d+\.\d+\.\d+|\[[0-9a-fA-F:]+\]|[0-9a-fA-F:]+):\d+\s+-\s+\d+\s+"([A-Z]+)\s+IN\s+([^"\s]+).*"\s+([A-Z]+).*\s([0-9.]+)s`)

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
	match := logPattern.FindStringSubmatch(line)
	if len(match) == 0 {
		return
	}
	clientIP := strings.Trim(match[1], "[]")
	queryType := match[2]
	domain := strings.TrimSuffix(strings.ToLower(match[3]), ".")
	latencySeconds, _ := strconv.ParseFloat(match[5], 64)

	blocked, source := coredns.IsBlocked(ctx, t.Store, domain)
	action := "allowed"
	if blocked {
		action = "blocked"
	}
	if source == "unknown" && strings.HasSuffix(domain, ".home") {
		source = "local"
	} else if source == "unknown" {
		source = "upstream"
	}

	_, err := t.Store.DB.ExecContext(ctx, `INSERT INTO dns_queries(timestamp, client_ip, domain, query_type, action, source, latency_ms) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), clientIP, domain, queryType, action, source, latencySeconds*1000)
	if err != nil {
		log.Printf("insert dns query failed: %v", err)
	}
}
