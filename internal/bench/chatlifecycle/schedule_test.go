package chatlifecycle

import (
	"errors"
	"math"
	"testing"
	"time"
)

func newScheduleTestModel(t *testing.T) ScheduleModel {
	t.Helper()
	cfg := DefaultConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	model, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel() error = %v", err)
	}
	return model
}

func TestScheduleDistributionMatchesExactOrdinalShares(t *testing.T) {
	model := newScheduleTestModel(t)
	const samples = 100_000

	var loginCounts [2]int
	var sessionCounts [4]int
	var lifecycleCounts [4]int
	var sessionTotal time.Duration
	for ordinal := uint64(0); ordinal < samples; ordinal++ {
		login, err := model.Login(ordinal)
		if err != nil {
			t.Fatalf("Login(%d) error = %v", ordinal, err)
		}
		loginCounts[login.Identity]++
		sessionCounts[login.SessionBucket]++
		sessionTotal += login.SessionDuration

		channel, err := model.Channel(ordinal, ordinal, ordinal+1)
		if err != nil {
			t.Fatalf("Channel(%d) error = %v", ordinal, err)
		}
		lifecycleCounts[channel.Class]++
	}

	if got, want := loginCounts, [2]int{80_000, 20_000}; got != want {
		t.Fatalf("login counts = %v, want %v", got, want)
	}
	if got, want := sessionCounts, [4]int{25_000, 50_000, 20_000, 5_000}; got != want {
		t.Fatalf("session counts = %v, want %v", got, want)
	}
	if got, want := lifecycleCounts, [4]int{60_000, 25_000, 10_000, 5_000}; got != want {
		t.Fatalf("lifecycle counts = %v, want %v", got, want)
	}
	mean := sessionTotal / samples
	if mean < 45*time.Minute || mean > 47*time.Minute {
		t.Fatalf("session mean = %v, want approximately 46m", mean)
	}

	firstLogin, err := model.Login(9_731)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	secondLogin, err := model.Login(9_731)
	if err != nil {
		t.Fatalf("Login() repeated error = %v", err)
	}
	if firstLogin != secondLogin {
		t.Fatalf("Login() is not deterministic: first=%+v second=%+v", firstLogin, secondLogin)
	}
	firstChannel, err := model.Channel(4_829, 1_103, 1_107)
	if err != nil {
		t.Fatalf("Channel() error = %v", err)
	}
	secondChannel, err := model.Channel(4_829, 1_103, 1_107)
	if err != nil {
		t.Fatalf("Channel() repeated error = %v", err)
	}
	if firstChannel != secondChannel {
		t.Fatalf("Channel() is not deterministic: first=%+v second=%+v", firstChannel, secondChannel)
	}
}

func TestScheduleDistributionValuesStayInsideSelectedBuckets(t *testing.T) {
	model := newScheduleTestModel(t)
	cfg := DefaultConfig()
	for ordinal := uint64(0); ordinal < 10_000; ordinal++ {
		login, err := model.Login(ordinal)
		if err != nil {
			t.Fatalf("Login(%d) error = %v", ordinal, err)
		}
		bucket := cfg.Workload.Sessions[login.SessionBucket]
		if login.SessionDuration < bucket.Min || login.SessionDuration > bucket.Max {
			t.Fatalf("Login(%d) duration = %v, want [%v,%v]", ordinal, login.SessionDuration, bucket.Min, bucket.Max)
		}

		channel, err := model.Channel(ordinal, ordinal, ordinal+1)
		if err != nil {
			t.Fatalf("Channel(%d) error = %v", ordinal, err)
		}
		switch channel.Class {
		case LifecycleOneShot:
			if channel.ActiveFor != 0 || channel.RevisitAfter != 0 || channel.RevisitMessages != 0 {
				t.Fatalf("one-shot schedule has later activity: %+v", channel)
			}
		case LifecycleRevisit:
			if channel.RevisitAfter < 10*time.Minute || channel.RevisitAfter > 60*time.Minute {
				t.Fatalf("revisit delay = %v, want [10m,60m]", channel.RevisitAfter)
			}
			if channel.RevisitMessages < 2 || channel.RevisitMessages > 5 {
				t.Fatalf("revisit messages = %d, want [2,5]", channel.RevisitMessages)
			}
			if channel.ActiveFor != 0 || !channel.RequiresColdRuntimeEvidence {
				t.Fatalf("revisit evidence contract = %+v", channel)
			}
		case LifecycleRotating:
			if channel.ActiveFor < 20*time.Minute || channel.ActiveFor > 40*time.Minute {
				t.Fatalf("rotating active duration = %v, want [20m,40m]", channel.ActiveFor)
			}
			if channel.RevisitAfter != 0 || channel.RevisitMessages != 0 {
				t.Fatalf("rotating schedule has revisit activity: %+v", channel)
			}
		case LifecycleLong:
			if channel.ActiveFor < 2*time.Hour || channel.ActiveFor > 4*time.Hour {
				t.Fatalf("long active duration = %v, want [2h,4h]", channel.ActiveFor)
			}
			if channel.RevisitAfter != 0 || channel.RevisitMessages != 0 {
				t.Fatalf("long schedule has revisit activity: %+v", channel)
			}
		default:
			t.Fatalf("unknown lifecycle class %d", channel.Class)
		}
		if !channel.NaturalCooling {
			t.Fatalf("Channel(%d) must cool naturally: %+v", ordinal, channel)
		}
	}
}

func TestInitialBurstScheduleCoversWindowWithBothEndpointsOnline(t *testing.T) {
	model := newScheduleTestModel(t)
	for ordinal := uint64(0); ordinal < 10_000; ordinal++ {
		channel, err := model.Channel(ordinal, ordinal, ordinal+1)
		if err != nil {
			t.Fatalf("Channel(%d) error = %v", ordinal, err)
		}
		burst := channel.InitialBurst
		if burst.MessageCount < 2 || burst.MessageCount > 8 {
			t.Fatalf("initial message count = %d, want [2,8]", burst.MessageCount)
		}
		if burst.Window < 5*time.Second || burst.Window > 30*time.Second {
			t.Fatalf("initial window = %v, want [5s,30s]", burst.Window)
		}
		if !burst.BothEndpointsOnline {
			t.Fatalf("initial burst must require both endpoints online: %+v", burst)
		}
		previous := time.Duration(-1)
		for message := 0; message < burst.MessageCount; message++ {
			offset, err := burst.MessageOffset(message)
			if err != nil {
				t.Fatalf("MessageOffset(%d) error = %v", message, err)
			}
			if offset < 0 || offset > burst.Window || offset <= previous {
				t.Fatalf("MessageOffset(%d) = %v after %v, window %v", message, offset, previous, burst.Window)
			}
			previous = offset
		}
		first, _ := burst.MessageOffset(0)
		last, _ := burst.MessageOffset(burst.MessageCount - 1)
		if first != 0 || last != burst.Window {
			t.Fatalf("initial offsets = [%v,%v], want [0,%v]", first, last, burst.Window)
		}
	}
}

func TestScheduleArrivalRatesSeparateNewUsersFromAllLogins(t *testing.T) {
	model := newScheduleTestModel(t)
	rates := model.LoginRates()
	if got, want := rates.NewUsersPerSecond, 250_000.0/86_400.0; math.Abs(got-want) > 1e-12 {
		t.Fatalf("new users/s = %.12f, want %.12f", got, want)
	}
	if got, want := rates.TotalLoginsPerSecond, (250_000.0*100.0/80.0)/86_400.0; math.Abs(got-want) > 1e-12 {
		t.Fatalf("total logins/s = %.12f, want %.12f", got, want)
	}
	if rates.NewUsersPerSecond < 2.89 || rates.NewUsersPerSecond > 2.90 {
		t.Fatalf("new users/s = %.4f, want approximately 2.9", rates.NewUsersPerSecond)
	}
	if rates.TotalLoginsPerSecond < 3.61 || rates.TotalLoginsPerSecond > 3.62 {
		t.Fatalf("total logins/s = %.4f, want approximately 3.6", rates.TotalLoginsPerSecond)
	}
}

func TestScheduleRejectsInvalidInputsAndBoundsOffsets(t *testing.T) {
	cfg := DefaultConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}

	if _, err := NewScheduleModel(nil, cfg.Workload); !errors.Is(err, errScheduleIdentityRequired) {
		t.Fatalf("NewScheduleModel(nil) error = %v, want %v", err, errScheduleIdentityRequired)
	}
	invalidNewUsers := cfg.Workload
	invalidNewUsers.NewUsersPerDay = 0
	if _, err := NewScheduleModel(identity, invalidNewUsers); err == nil {
		t.Fatal("NewScheduleModel() accepted zero new users per day")
	}
	invalidLogin := cfg.Workload
	invalidLogin.Login = LoginDistribution{NewPercent: 0, ReturningPercent: 100}
	if _, err := NewScheduleModel(identity, invalidLogin); err == nil {
		t.Fatal("NewScheduleModel() accepted a zero new-login share")
	}
	invalidBurst := cfg.Workload
	invalidBurst.Relationship.InitialMessages.Min = 1
	if _, err := NewScheduleModel(identity, invalidBurst); !errors.Is(err, errScheduleInitialMessageRange) {
		t.Fatalf("NewScheduleModel(initial minimum below two) error = %v, want %v", err, errScheduleInitialMessageRange)
	}

	model := newScheduleTestModel(t)
	if _, err := model.Channel(0, 7, 7); !errors.Is(err, errScheduleEndpointOrder) {
		t.Fatalf("Channel(equal endpoints) error = %v, want %v", err, errScheduleEndpointOrder)
	}
	if _, err := model.Channel(0, 8, 7); !errors.Is(err, errScheduleEndpointOrder) {
		t.Fatalf("Channel(reversed endpoints) error = %v, want %v", err, errScheduleEndpointOrder)
	}
	channel, err := model.Channel(math.MaxUint64, math.MaxUint64-1, math.MaxUint64)
	if err != nil {
		t.Fatalf("Channel(max uint64 boundary) error = %v", err)
	}
	if _, err := channel.InitialBurst.MessageOffset(-1); !errors.Is(err, errScheduleMessageIndex) {
		t.Fatalf("MessageOffset(-1) error = %v, want %v", err, errScheduleMessageIndex)
	}
	if _, err := channel.InitialBurst.MessageOffset(channel.InitialBurst.MessageCount); !errors.Is(err, errScheduleMessageIndex) {
		t.Fatalf("MessageOffset(count) error = %v, want %v", err, errScheduleMessageIndex)
	}
	invalidWindow := InitialBurstSchedule{MessageCount: 2, Window: 0, BothEndpointsOnline: true}
	if _, err := invalidWindow.MessageOffset(0); !errors.Is(err, errScheduleMessageWindow) {
		t.Fatalf("MessageOffset(zero window) error = %v, want %v", err, errScheduleMessageWindow)
	}
	insufficientWindow := InitialBurstSchedule{MessageCount: 3, Window: time.Nanosecond, BothEndpointsOnline: true}
	if _, err := insufficientWindow.MessageOffset(0); !errors.Is(err, errScheduleMessageSpacing) {
		t.Fatalf("MessageOffset(insufficient window) error = %v, want %v", err, errScheduleMessageSpacing)
	}
}

func TestScheduleRejectsMessageRangesOutsideApprovedBounds(t *testing.T) {
	cfg := DefaultConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*WorkloadConfig)
		wantErr error
	}{
		{
			name: "initial maximum above eight",
			mutate: func(workload *WorkloadConfig) {
				workload.Relationship.InitialMessages = IntRange{Min: 2, Max: 9}
			},
			wantErr: errScheduleInitialMessageRange,
		},
		{
			name: "returning minimum below two",
			mutate: func(workload *WorkloadConfig) {
				workload.Relationship.ReturningMessages = IntRange{Min: 1, Max: 5}
			},
			wantErr: errScheduleReturningMessageRange,
		},
		{
			name: "returning maximum above five",
			mutate: func(workload *WorkloadConfig) {
				workload.Relationship.ReturningMessages = IntRange{Min: 2, Max: 6}
			},
			wantErr: errScheduleReturningMessageRange,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workload := cfg.Workload
			tt.mutate(&workload)
			if _, err := NewScheduleModel(identity, workload); !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewScheduleModel() error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	workload := cfg.Workload
	workload.Relationship.InitialMessages = IntRange{Min: 2, Max: 8}
	workload.Relationship.ReturningMessages = IntRange{Min: 2, Max: 5}
	if _, err := NewScheduleModel(identity, workload); err != nil {
		t.Fatalf("NewScheduleModel(approved boundaries) error = %v", err)
	}
}

func TestScheduleHandlesMaximumDurationWithoutOverflow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Workload.Sessions = []DurationShare{{Percent: 100, Min: time.Duration(math.MaxInt64), Max: time.Duration(math.MaxInt64)}}
	cfg.Workload.Lifecycle.OneShot.Percent = 0
	cfg.Workload.Lifecycle.Revisit.Percent = 0
	cfg.Workload.Lifecycle.Rotating = LifecycleBucket{Percent: 100, ActiveDuration: DurationRange{Min: time.Duration(math.MaxInt64), Max: time.Duration(math.MaxInt64)}}
	cfg.Workload.Lifecycle.Long.Percent = 0
	cfg.Workload.Relationship.InitialMessageWindow = DurationRange{Min: time.Duration(math.MaxInt64), Max: time.Duration(math.MaxInt64)}
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	model, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel() error = %v", err)
	}
	login, err := model.Login(math.MaxUint64)
	if err != nil {
		t.Fatalf("Login(max uint64) error = %v", err)
	}
	if login.SessionDuration != time.Duration(math.MaxInt64) {
		t.Fatalf("session duration = %v, want MaxInt64", login.SessionDuration)
	}
	channel, err := model.Channel(math.MaxUint64, math.MaxUint64-1, math.MaxUint64)
	if err != nil {
		t.Fatalf("Channel(max duration) error = %v", err)
	}
	if channel.ActiveFor != time.Duration(math.MaxInt64) || channel.InitialBurst.Window != time.Duration(math.MaxInt64) {
		t.Fatalf("maximum-duration schedule = %+v", channel)
	}
	last, err := channel.InitialBurst.MessageOffset(channel.InitialBurst.MessageCount - 1)
	if err != nil || last != time.Duration(math.MaxInt64) {
		t.Fatalf("last maximum-duration offset = %v, %v", last, err)
	}
}

func TestScheduleDiscreteSequencesRotateAcrossFixedRunKeys(t *testing.T) {
	cfg := DefaultConfig()
	firstIdentity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace(first) error = %v", err)
	}
	secondIdentity, err := NewIdentitySpace(cfg.RunID+"-other", cfg.Seed+1, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace(second) error = %v", err)
	}
	first, err := NewScheduleModel(firstIdentity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel(first) error = %v", err)
	}
	second, err := NewScheduleModel(secondIdentity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel(second) error = %v", err)
	}

	type discreteSequences struct {
		loginIdentity [100]LoginIdentity
		sessionBucket [100]int
		lifecycle     [100]LifecycleClass
	}
	derive := func(name string, model ScheduleModel) discreteSequences {
		t.Helper()
		var result discreteSequences
		for ordinal := uint64(0); ordinal < 100; ordinal++ {
			login, err := model.Login(ordinal)
			if err != nil {
				t.Fatalf("%s Login(%d) error = %v", name, ordinal, err)
			}
			channel, err := model.Channel(ordinal, ordinal, ordinal+1)
			if err != nil {
				t.Fatalf("%s Channel(%d) error = %v", name, ordinal, err)
			}
			result.loginIdentity[ordinal] = login.Identity
			result.sessionBucket[ordinal] = login.SessionBucket
			result.lifecycle[ordinal] = channel.Class
		}
		return result
	}
	firstSequences := derive("first", first)
	secondSequences := derive("second", second)
	// These fixed non-secret keys deliberately produce distinct phase draws for
	// all three semantic streams; comparing full cycles excludes duration draws.
	if firstSequences.loginIdentity == secondSequences.loginIdentity {
		t.Fatal("login identity ordinal cycle did not rotate across fixed run keys")
	}
	if firstSequences.sessionBucket == secondSequences.sessionBucket {
		t.Fatal("session bucket ordinal cycle did not rotate across fixed run keys")
	}
	if firstSequences.lifecycle == secondSequences.lifecycle {
		t.Fatal("lifecycle class ordinal cycle did not rotate across fixed run keys")
	}
}

func TestScheduleModelComputesGlobalNewOrdinalFromLoginPrefix(t *testing.T) {
	cfg := FormalConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	model, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel: %v", err)
	}
	if model.loginPhase != 79 {
		t.Fatalf("formal login phase = %d, want fixed regression phase 79", model.loginPhase)
	}

	for loginOrdinal, want := range map[uint64]uint64{
		0: 0, 1: 1, 21: 1, 22: 2, 100: 80, 312_500: 250_000,
	} {
		if got := model.NewOrdinalBefore(loginOrdinal); got != want {
			t.Fatalf("NewOrdinalBefore(%d) = %d, want %d", loginOrdinal, got, want)
		}
	}
	for loginOrdinal, want := range map[uint64]uint64{0: 0, 21: 1, 99: 79, 100: 80} {
		login, loginErr := model.Login(loginOrdinal)
		if loginErr != nil || login.Identity != LoginNew || login.NewOrdinal != want {
			t.Fatalf("Login(%d) = %+v, %v; want new ordinal %d", loginOrdinal, login, loginErr, want)
		}
	}
}

func TestScheduleModelResolvesWorkerLocalNewOrdinalsAcrossPhasesAndWorkers(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		runID   string
		seed    uint64
		workers uint64
	}{
		{name: "formal_default", runID: FormalConfig().RunID, seed: FormalConfig().Seed, workers: 3},
		{name: "seed_40", runID: "phase-repro", seed: 40, workers: 3},
		{name: "one_hundred_by_four", runID: "resolver-100x4", seed: 71, workers: 4},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := FormalConfig()
			cfg.RunID = testCase.runID
			cfg.Seed = testCase.seed
			cfg.Workload.Workers = int(testCase.workers)
			identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, testCase.workers)
			if err != nil {
				t.Fatalf("NewIdentitySpace: %v", err)
			}
			model, err := NewScheduleModel(identity, cfg.Workload)
			if err != nil {
				t.Fatalf("NewScheduleModel: %v", err)
			}

			localCounts := make([]uint64, testCase.workers)
			for loginOrdinal := uint64(0); ; loginOrdinal++ {
				complete := true
				for _, count := range localCounts {
					if count < 100 {
						complete = false
						break
					}
				}
				if complete {
					break
				}
				login, loginErr := model.Login(loginOrdinal)
				if loginErr != nil {
					t.Fatalf("Login(%d): %v", loginOrdinal, loginErr)
				}
				if login.Identity != LoginNew {
					continue
				}
				workerID := loginOrdinal % testCase.workers
				localNewIndex := localCounts[workerID]
				if localNewIndex < 100 {
					got, resolveErr := model.GlobalNewOrdinalFor(workerID, localNewIndex)
					if resolveErr != nil {
						t.Fatalf("GlobalNewOrdinalFor(%d, %d): %v", workerID, localNewIndex, resolveErr)
					}
					if got != login.NewOrdinal {
						t.Fatalf("worker %d local new %d resolved %d, want login %d new ordinal %d", workerID, localNewIndex, got, loginOrdinal, login.NewOrdinal)
					}
				}
				localCounts[workerID]++
			}
		})
	}
}

func TestScheduleModelCopiesSessionBucketsAtConstruction(t *testing.T) {
	cfg := DefaultConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	model, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel() error = %v", err)
	}
	var before [100]LoginSchedule
	for ordinal := uint64(0); ordinal < uint64(len(before)); ordinal++ {
		before[ordinal], err = model.Login(ordinal)
		if err != nil {
			t.Fatalf("Login(%d) before mutation error = %v", ordinal, err)
		}
	}
	mutated := []DurationShare{
		{Percent: 50, Min: time.Minute, Max: 2 * time.Minute},
		{Percent: 25, Min: 2 * time.Minute, Max: 3 * time.Minute},
		{Percent: 20, Min: 3 * time.Minute, Max: 4 * time.Minute},
		{Percent: 5, Min: 4 * time.Minute, Max: 5 * time.Minute},
	}
	copy(cfg.Workload.Sessions, mutated)
	for ordinal := uint64(0); ordinal < uint64(len(before)); ordinal++ {
		after, err := model.Login(ordinal)
		if err != nil {
			t.Fatalf("Login(%d) after mutation error = %v", ordinal, err)
		}
		if after != before[ordinal] {
			t.Fatalf("Login(%d) changed after source mutation: before=%+v after=%+v", ordinal, before[ordinal], after)
		}
	}
}
