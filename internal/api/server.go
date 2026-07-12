package api

import (
	"net/http"

	"github.com/derek/faro/internal/api/handlers"
	"github.com/derek/faro/internal/db"
)

type CoreDNSManager = handlers.CoreDNSManager

func NewServer(store *db.Store, reloader CoreDNSManager) http.Handler {
	return handlers.New(store, reloader)
}
