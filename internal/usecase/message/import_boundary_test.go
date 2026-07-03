package message_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

func TestMessageUsecaseImportBoundary(t *testing.T) {
	forbidden := []string{
		"github.com/WuKongIM/WuKongIM/pkg/gateway",
		"github.com/WuKongIM/WuKongIM/pkg/protocol/frame",
		"github.com/WuKongIM/WuKongIM/pkg/cluster",
		"github.com/WuKongIM/WuKongIM/pkg/channel",
		"github.com/WuKongIM/WuKongIM/internal/access",
		"github.com/WuKongIM/WuKongIM/internal/app",
	}
	files, err := parser.ParseDir(token.NewFileSet(), ".", func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseDir() error = %v", err)
	}
	for _, pkg := range files {
		for filename, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if path == bad || strings.HasPrefix(path, bad+"/") {
						t.Fatalf("%s imports forbidden package %q", filename, path)
					}
				}
			}
		}
	}
}

func TestMessagePackageAliasesSendFacadeTypes(t *testing.T) {
	var _ channelappend.SendCommand = message.SendCommand{}
	var _ channelappend.SendBatchItem = message.SendBatchItem{}
	var _ channelappend.SendBatchItemResult = message.SendBatchItemResult{}

	reasons := []struct {
		name     string
		message  message.Reason
		contract channelappend.Reason
	}{
		{name: "success", message: message.ReasonSuccess, contract: channelappend.ReasonSuccess},
		{name: "invalid request", message: message.ReasonInvalidRequest, contract: channelappend.ReasonInvalidRequest},
		{name: "auth fail", message: message.ReasonAuthFail, contract: channelappend.ReasonAuthFail},
		{name: "channel not exist", message: message.ReasonChannelNotExist, contract: channelappend.ReasonChannelNotExist},
		{name: "node not match", message: message.ReasonNodeNotMatch, contract: channelappend.ReasonNodeNotMatch},
		{name: "system error", message: message.ReasonSystemError, contract: channelappend.ReasonSystemError},
		{name: "unsupported", message: message.ReasonUnsupported, contract: channelappend.ReasonUnsupported},
		{name: "subscriber not exist", message: message.ReasonSubscriberNotExist, contract: channelappend.ReasonSubscriberNotExist},
		{name: "in blacklist", message: message.ReasonInBlacklist, contract: channelappend.ReasonInBlacklist},
		{name: "not allow send", message: message.ReasonNotAllowSend, contract: channelappend.ReasonNotAllowSend},
		{name: "not in whitelist", message: message.ReasonNotInWhitelist, contract: channelappend.ReasonNotInWhitelist},
		{name: "ban", message: message.ReasonBan, contract: channelappend.ReasonBan},
		{name: "disband", message: message.ReasonDisband, contract: channelappend.ReasonDisband},
		{name: "send ban", message: message.ReasonSendBan, contract: channelappend.ReasonSendBan},
	}
	for _, reason := range reasons {
		if reason.message != reason.contract {
			t.Fatalf("%s reason alias mismatch: message=%d contract=%d", reason.name, reason.message, reason.contract)
		}
	}
}
