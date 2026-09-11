package migration

import (
	"context"
	"fmt"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestSearchProfileRequiresExactProgramAndRegistration(t *testing.T) {
	for _, fault := range []string{"valid", "binary", "mixed-profile", "missing-program", "competition", "unmapped-config", "method", "config", "status"} {
		t.Run(fault, func(t *testing.T) {
			ctx, w := context.Background(), dedupeTestWorkspace(t)
			p := Plan{SourceCommit: "a888f89533d0e7d1b2030e06504ca97f1ad891d4", PluginConfigs: []PluginConfigMapping{{PluginNo: "wk.plugin.search", SourceNode: 1}}}
			c := SourceCapture{Digest: strings.Repeat("c", 64), Tables: map[string]uint64{"Plugin": 3}}
			for n := uint64(1); n <= 3; n++ {
				p.Sources = append(p.Sources, NodeOptions{NodeID: n})
				p.PluginArtifacts = append(p.PluginArtifacts, PluginArtifactSpec{SourceNode: n, PluginNo: "wk.plugin.search", Path: fmt.Sprintf("/source-%d/plugin", n), Bytes: 63324496, SHA256: "68079973350ec42480d84ee83770e5f7910fc06daeba41b746362e28492d939f", Profile: "wk-search-persist-route-linux-amd64-v1"})
			}
			switch fault {
			case "binary":
				p.PluginArtifacts[0].SHA256 = strings.Repeat("0", 64)
			case "mixed-profile":
				p.PluginArtifacts[0].Profile = AIExampleReceiveProfile
			case "missing-program":
				p.PluginArtifacts = p.PluginArtifacts[:2]
			case "competition":
				c.Tables["Plugin"]++
			case "unmapped-config":
				p.PluginConfigs = nil
			}
			for n := uint64(1); n <= 3; n++ {
				r := MappedPluginSettings{SourceNode: n, SourceRowSHA256: fmt.Sprintf("%064x", n), Original: SourcePlugin{No: "wk.plugin.search", Version: "0.0.1", Priority: 1, Methods: []string{"PersistAfter", "Route"}}}
				if n == 2 {
					switch fault {
					case "method":
						r.Original.Methods = []string{"Route"}
					case "config":
						r.Original.Config = []byte(`{"extra":true}`)
					case "status":
						r.Original.Status = 3
					}
				}
				data, err := MarshalState(r)
				require.NoError(t, err)
				key := "plugin-settings-original/v2/" + c.Digest + "/" + p.Digest() + fmt.Sprintf("/%020d/%x", n, []byte("wk.plugin.search"))
				require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: []byte(key), Value: data}}))
			}
			evidence, err := certifyPluginProfile(ctx, p, c, w)
			if fault != "valid" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "wk-search-persist-route-linux-amd64-v1", evidence.Profile)
			require.Len(t, evidence.SourceRows, 3)
		})
	}
}
