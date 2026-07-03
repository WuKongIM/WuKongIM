package raft

import (
	"encoding/json"
	"testing"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
)

func BenchmarkControllerCommandCodec(b *testing.B) {
	expected := controllermeta.OnboardingJobStatusPlanned
	job, assignment, task := sampleRaftOnboardingUpdate()
	cmd := slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeOnboardingJobUpdate,
		NodeOnboarding: &slotcontroller.NodeOnboardingJobUpdate{
			Job:            &job,
			ExpectedStatus: &expected,
			Assignment:     &assignment,
			Task:           &task,
		},
	}
	binaryData, err := encodeCommand(cmd)
	if err != nil {
		b.Fatalf("encodeCommand() error = %v", err)
	}
	jsonData, err := json.Marshal(commandEnvelopeFromCommand(cmd))
	if err != nil {
		b.Fatalf("json.Marshal() error = %v", err)
	}

	b.Run("binary_marshal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := encodeCommand(cmd); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := decodeCommand(binaryData); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("json_marshal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := json.Marshal(commandEnvelopeFromCommand(cmd)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := decodeCommand(jsonData); err != nil {
				b.Fatal(err)
			}
		}
	})
}
