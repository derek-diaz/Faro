package api

import (
	"net/http"

	"github.com/derek/faro/internal/api/handlers"
	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/upstreamhealth"
)

type CoreDNSManager = handlers.CoreDNSManager

func NewServer(store *db.Store, reloader CoreDNSManager, upstreams *upstreamhealth.Monitor) http.Handler {
	return handlers.New(store, reloader, upstreams)
}
