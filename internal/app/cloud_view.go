package app

import (
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/access/cloudview"
)

// NewCloudViewHandler composes the run-scoped public browser gateway without
// joining the WuKongIM cluster or holding cloud credentials.
func NewCloudViewHandler(options cloudview.Options) (http.Handler, error) {
	server, err := cloudview.New(options)
	if err != nil {
		return nil, err
	}
	return server.Handler(), nil
}
