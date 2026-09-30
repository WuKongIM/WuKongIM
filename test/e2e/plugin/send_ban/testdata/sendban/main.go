package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/wkrpc/client"
	rpc "github.com/WuKongIM/wkrpc/proto"
)

// result records the actual host status independently of transport errors.
type result struct {
	Status     uint16    `json:"status"`
	MessageID  int64     `json:"message_id"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

func main() {
	socket := flag.String("socket", "", "host Unix socket")
	sandbox := flag.String("sandbox", "", "plugin sandbox")
	flag.Parse()
	if err := run(*socket, *sandbox); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(socket, sandbox string) error {
	c := client.New("unix://"+socket, client.WithUid("sendban"))
	stopped := make(chan struct{}, 1)
	c.Route("/stop", func(ctx *client.Context) {
		ctx.WriteOk()
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	// Deliberately attempt identity/target replacement; only the payload is allowed.
	c.Route("/plugin/send", func(ctx *client.Context) {
		var packet pluginproto.SendPacket
		if err := packet.Unmarshal(ctx.Body()); err != nil {
			ctx.WriteErr(err)
			return
		}
		// The test controls a request already admitted by the host. This gate
		// proves the documented in-flight boundary without timing guesses.
		if string(packet.Payload) == "inflight-before-ban" {
			if err := os.WriteFile(filepath.Join(sandbox, "hook-entered"), []byte("entered"), 0600); err != nil {
				ctx.WriteErr(err)
				return
			}
			timeout := time.NewTimer(4 * time.Second)
			defer timeout.Stop()
			poll := time.NewTicker(10 * time.Millisecond)
			defer poll.Stop()
			for {
				if _, err := os.Stat(filepath.Join(sandbox, "hook-release")); err == nil {
					break
				} else if !os.IsNotExist(err) {
					ctx.WriteErr(err)
					return
				}
				select {
				case <-timeout.C:
					ctx.WriteErr(errors.New("hook release timeout"))
					return
				case <-poll.C:
				}
			}
		}
		packet.FromUid = "plugin-forged-sender"
		packet.ChannelId = "plugin-forged-target"
		packet.ChannelType = 2
		packet.Payload = append([]byte("hook:"), packet.Payload...)
		packet.Reason = 0
		raw, err := packet.Marshal()
		if err != nil {
			ctx.WriteErr(err)
			return
		}
		ctx.Write(raw)
	})
	if err := c.Start(); err != nil {
		return err
	}
	defer c.Stop()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for !c.IsAuthed() {
		select {
		case <-deadline.C:
			return errors.New("plugin authentication timeout")
		case <-ticker.C:
		}
	}
	body, err := (&pluginproto.PluginInfo{No: "sendban", Name: "Send ban E2E", Version: "1", Methods: []string{"Send"}}).Marshal()
	if err != nil {
		return err
	}
	resp, err := c.Request("/plugin/start", body)
	if err != nil {
		return err
	}
	if resp.Status != rpc.StatusOK {
		return fmt.Errorf("plugin start status %d: %s", resp.Status, resp.Body)
	}
	var startup pluginproto.StartupResp
	if err := startup.Unmarshal(resp.Body); err != nil {
		return err
	}
	if !startup.Success {
		return errors.New("plugin start rejected")
	}
	if err := os.WriteFile(filepath.Join(sandbox, "ready"), []byte("ready"), 0600); err != nil {
		return err
	}
	for i := 0; ; i++ {
		path := filepath.Join(sandbox, fmt.Sprintf("command-%03d.json", i))
		var raw []byte
		for {
			raw, err = os.ReadFile(path)
			if err == nil {
				break
			}
			if !os.IsNotExist(err) {
				return err
			}
			select {
			case <-stopped:
				return nil
			case <-ticker.C:
			}
		}
		var req pluginproto.SendReq
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
		body, err := req.Marshal()
		if err != nil {
			return err
		}
		out := result{StartedAt: time.Now().UTC()}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		reply, err := c.RequestWithContext(ctx, "/message/send", body)
		cancel()
		if err != nil {
			out.Error = err.Error()
		} else {
			out.Status = uint16(reply.Status)
			if reply.Status != rpc.StatusOK {
				out.Error = string(reply.Body)
			} else {
				var response pluginproto.SendResp
				if err := response.Unmarshal(reply.Body); err != nil {
					out.Error = err.Error()
				} else {
					out.MessageID = response.MessageId
				}
			}
		}
		out.FinishedAt = time.Now().UTC()
		encoded, err := json.Marshal(out)
		if err != nil {
			return err
		}
		dest := filepath.Join(sandbox, fmt.Sprintf("result-%03d.json", i))
		if err := os.WriteFile(dest+".tmp", encoded, 0600); err != nil {
			return err
		}
		if err := os.Rename(dest+".tmp", dest); err != nil {
			return err
		}
	}
}
