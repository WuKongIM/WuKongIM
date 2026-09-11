package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

type pluginRouteTestPorts struct {
	pluginusecase.Runtime
	pluginusecase.Invoker
	request      pluginproto.HttpRequest
	plugin, path string
}

func (p *pluginRouteTestPorts) RequestPlugin(_ context.Context, plugin, path string, data []byte) ([]byte, error) {
	p.plugin, p.path = plugin, path
	if err := p.request.Unmarshal(data); err != nil {
		return nil, err
	}
	return (&pluginproto.HttpResponse{Status: 200, Body: []byte(`{"messages":[]}`)}).Marshal()
}

func TestProductAPIWiresPluginRouteThroughPluginUsecase(t *testing.T) {
	ports := &pluginRouteTestPorts{}
	plugins, err := pluginusecase.NewApp(pluginusecase.Options{Runtime: ports, Invoker: ports})
	require.NoError(t, err)
	a := &App{cfg: Config{API: APIConfig{ListenAddr: "127.0.0.1:0"}, Plugin: PluginConfig{Timeout: time.Second}}, plugins: plugins, logger: wklog.NewNop()}
	a.wireAPI()
	srv, ok := a.api.(*accessapi.Server)
	require.True(t, ok)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/plugins/wk.plugin.search/search", strings.NewReader(`{"limit":1}`)))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, `{"messages":[]}`, rec.Body.String())
	require.Equal(t, "wk.plugin.search", ports.plugin)
	require.Equal(t, pluginusecase.PathRoute, ports.path)
	require.Equal(t, "/search", ports.request.Path)
	require.Equal(t, []byte(`{"limit":1}`), ports.request.Body)
}
