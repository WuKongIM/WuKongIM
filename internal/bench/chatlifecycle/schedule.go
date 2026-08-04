package chatlifecycle

import (
	"errors"
	"math"
	"math/bits"
	"time"
)

const (
	distributionCycle      = uint64(100)
	secondsPerDay          = 24 * 60 * 60
	minimumRevisitDelay    = 10 * time.Minute
	maximumRevisitDelay    = 60 * time.Minute
	minimumInitialMessages = 2
	maximumInitialMessages = 8
	minimumRevisitMessages = 2
	maximumRevisitMessages = 5
)

var (
	errScheduleIdentityRequired      = errors.New("chat lifecycle schedule: identity space is required")
	errScheduleNewUsersPerDay        = errors.New("chat lifecycle schedule: new users per day must be positive")
	errScheduleNewLoginShare         = errors.New("chat lifecycle schedule: new login share must be positive")
	errScheduleInitialMessageRange   = errors.New("chat lifecycle schedule: initial message range must stay within 2..8")
	errScheduleReturningMessageRange = errors.New("chat lifecycle schedule: returning message range must stay within 2..5")
	errScheduleMessageSpacing        = errors.New("chat lifecycle schedule: initial message window cannot space every message")
	errScheduleEndpointOrder         = errors.New("chat lifecycle schedule: person endpoints must be distinct and ordered lower to higher")
	errScheduleMessageIndex          = errors.New("chat lifecycle schedule: message index is outside the initial burst")
	errScheduleMessageWindow         = errors.New("chat lifecycle schedule: initial burst window must be positive")
	errScheduleNewOrdinal            = errors.New("chat lifecycle schedule: global new-user ordinal is outside the login stream")
)

// LoginIdentity identifies whether a login introduces a new UID or reuses a
// real reconstructable historical UID selected by later worker logic.
type LoginIdentity uint8

const (
	// LoginNew introduces the next never-before-seen deterministic UID.
	LoginNew LoginIdentity = iota
	// LoginReturning selects a historical UID without claiming it is offline.
	LoginReturning
)

// LifecycleClass is the finite activity shape assigned to a new person channel.
type LifecycleClass uint8

const (
	// LifecycleOneShot ends after the initial burst.
	LifecycleOneShot LifecycleClass = iota
	// LifecycleRevisit returns after natural runtime eviction.
	LifecycleRevisit
	// LifecycleRotating remains actively scheduled for a short bounded interval.
	LifecycleRotating
	// LifecycleLong remains actively scheduled for a longer bounded interval.
	LifecycleLong
)

// LoginSchedule is a pure login decision reconstructed from one global login
// ordinal. SessionBucket indexes the validated WorkloadConfig.Sessions slice.
type LoginSchedule struct {
	Identity LoginIdentity
	// NewOrdinal is the count of LoginNew decisions before this global login.
	// It is this login's zero-based global new-user ordinal when Identity is new.
	NewOrdinal      uint64
	SessionBucket   int
	SessionDuration time.Duration
}

// LoginRates distinguishes identity growth from the larger login stream that
// also contains returning users.
type LoginRates struct {
	NewUsersPerSecond    float64
	TotalLoginsPerSecond float64
}

// InitialBurstSchedule describes finite initial SEND activity. The first
// message is at offset zero and the last is at Window; no polling is modeled.
type InitialBurstSchedule struct {
	MessageCount        int
	Window              time.Duration
	BothEndpointsOnline bool
}

// MessageOffset returns the evenly spaced offset of one initial message using
// checked 128-bit multiplication so even valid maximum durations cannot wrap.
func (s InitialBurstSchedule) MessageOffset(messageIndex int) (time.Duration, error) {
	if messageIndex < 0 || messageIndex >= s.MessageCount || s.MessageCount < minimumInitialMessages {
		return 0, errScheduleMessageIndex
	}
	if s.Window <= 0 {
		return 0, errScheduleMessageWindow
	}
	if uint64(s.MessageCount-1) > uint64(s.Window) {
		return 0, errScheduleMessageSpacing
	}
	segments := uint64(s.MessageCount - 1)
	high, low := bits.Mul64(uint64(s.Window), uint64(messageIndex))
	offset, _ := bits.Div64(high, low, segments)
	return time.Duration(offset), nil
}

// ChannelSchedule contains only bounded SEND windows. NaturalCooling requires
// the worker to stop activity after the schedule; it must never poll a Channel
// runtime to keep it loaded. A revisit requires prior all-node cold evidence.
type ChannelSchedule struct {
	Class                       LifecycleClass
	InitialBurst                InitialBurstSchedule
	ActiveFor                   time.Duration
	RevisitAfter                time.Duration
	RevisitMessages             int
	RequiresColdRuntimeEvidence bool
	NaturalCooling              bool
}

// ScheduleModel is an immutable, history-independent session and channel
// planner. Every draw is keyed by the run identity and a semantic purpose.
type ScheduleModel struct {
	identity          *IdentitySpace
	newUsersPerDay    int
	login             LoginDistribution
	sessions          []DurationShare
	sessionPercents   []int
	lifecycle         LifecycleDistribution
	lifecyclePercents [4]int
	relationship      RelationshipConfig
	loginPhase        uint64
	sessionPhase      uint64
	lifecyclePhase    uint64
}

// NewScheduleModel copies validated scheduling inputs so caller mutation cannot
// change replay. It performs no I/O and starts no background work.
func NewScheduleModel(identity *IdentitySpace, workload WorkloadConfig) (ScheduleModel, error) {
	if identity == nil {
		return ScheduleModel{}, errScheduleIdentityRequired
	}
	if workload.NewUsersPerDay <= 0 {
		return ScheduleModel{}, errScheduleNewUsersPerDay
	}
	if err := validatePercentPair("workload.login", workload.Login.NewPercent, workload.Login.ReturningPercent); err != nil {
		return ScheduleModel{}, err
	}
	if workload.Login.NewPercent == 0 {
		return ScheduleModel{}, errScheduleNewLoginShare
	}
	if err := validateDurationShares("workload.sessions", workload.Sessions, true); err != nil {
		return ScheduleModel{}, err
	}
	if err := validateLifecycle(workload.Lifecycle); err != nil {
		return ScheduleModel{}, err
	}
	if err := validateIntRange("workload.relationship.initial_messages", workload.Relationship.InitialMessages); err != nil {
		return ScheduleModel{}, err
	}
	if workload.Relationship.InitialMessages.Min < minimumInitialMessages || workload.Relationship.InitialMessages.Max > maximumInitialMessages {
		return ScheduleModel{}, errScheduleInitialMessageRange
	}
	if err := validateDurationRange("workload.relationship.initial_message_window", workload.Relationship.InitialMessageWindow); err != nil {
		return ScheduleModel{}, err
	}
	if uint64(workload.Relationship.InitialMessages.Max-1) > uint64(workload.Relationship.InitialMessageWindow.Min) {
		return ScheduleModel{}, errScheduleMessageSpacing
	}
	if err := validateIntRange("workload.relationship.returning_messages", workload.Relationship.ReturningMessages); err != nil {
		return ScheduleModel{}, err
	}
	if workload.Relationship.ReturningMessages.Min < minimumRevisitMessages || workload.Relationship.ReturningMessages.Max > maximumRevisitMessages {
		return ScheduleModel{}, errScheduleReturningMessageRange
	}

	loginPhase, err := identity.decisionBelow("login-identity-ordinal-phase/v1", distributionCycle)
	if err != nil {
		return ScheduleModel{}, err
	}
	sessionPhase, err := identity.decisionBelow("session-bucket-ordinal-phase/v1", distributionCycle)
	if err != nil {
		return ScheduleModel{}, err
	}
	lifecyclePhase, err := identity.decisionBelow("lifecycle-class-ordinal-phase/v1", distributionCycle)
	if err != nil {
		return ScheduleModel{}, err
	}

	sessions := append([]DurationShare(nil), workload.Sessions...)
	sessionPercents := make([]int, len(sessions))
	for index := range sessions {
		sessionPercents[index] = sessions[index].Percent
	}
	return ScheduleModel{
		identity:        identity,
		newUsersPerDay:  workload.NewUsersPerDay,
		login:           workload.Login,
		sessions:        sessions,
		sessionPercents: sessionPercents,
		lifecycle:       workload.Lifecycle,
		lifecyclePercents: [4]int{
			workload.Lifecycle.OneShot.Percent,
			workload.Lifecycle.Revisit.Percent,
			workload.Lifecycle.Rotating.Percent,
			workload.Lifecycle.Long.Percent,
		},
		relationship:   workload.Relationship,
		loginPhase:     loginPhase,
		sessionPhase:   sessionPhase,
		lifecyclePhase: lifecyclePhase,
	}, nil
}

// Login reconstructs identity kind, session bucket, and session duration for
// one global login ordinal without consuming shared random state.
func (m ScheduleModel) Login(loginOrdinal uint64) (LoginSchedule, error) {
	identity := LoginReturning
	if ordinalPercent(loginOrdinal, m.loginPhase) < m.login.NewPercent {
		identity = LoginNew
	}
	sessionBucket := percentBucket(ordinalPercent(loginOrdinal, m.sessionPhase), m.sessionPercents)
	duration, err := m.durationInRange("session-duration/v1", m.sessions[sessionBucket].Min, m.sessions[sessionBucket].Max, loginOrdinal, uint64(sessionBucket))
	if err != nil {
		return LoginSchedule{}, err
	}
	return LoginSchedule{
		Identity: identity, NewOrdinal: m.NewOrdinalBefore(loginOrdinal),
		SessionBucket: sessionBucket, SessionDuration: duration,
	}, nil
}

// NewOrdinalBefore counts LoginNew decisions in [0, loginOrdinal). The fixed
// 100-position cycle makes the prefix calculation O(1) and history-independent.
func (m ScheduleModel) NewOrdinalBefore(loginOrdinal uint64) uint64 {
	result := (loginOrdinal / distributionCycle) * uint64(m.login.NewPercent)
	for position := uint64(0); position < loginOrdinal%distributionCycle; position++ {
		if ordinalPercent(position, m.loginPhase) < m.login.NewPercent {
			result++
		}
	}
	return result
}

// GlobalNewOrdinalFor resolves a worker-local new identity to the same global
// new-user ordinal used by Login. The login and worker cycles repeat within at
// most 100 lane positions, so lookup is bounded and retains no runtime history.
func (m ScheduleModel) GlobalNewOrdinalFor(workerID, localNewIndex uint64) (uint64, error) {
	if m.identity == nil {
		return 0, errScheduleIdentityRequired
	}
	workerCount := m.identity.Workers()
	if workerCount == 0 || workerID >= workerCount {
		return 0, errScheduleNewOrdinal
	}
	lanePeriod := distributionCycle / greatestCommonDivisor(distributionCycle, workerCount)
	if workerCount > math.MaxUint64/lanePeriod {
		return 0, errScheduleNewOrdinal
	}
	supercycleLogins := workerCount * lanePeriod
	cyclesPerSupercycle := supercycleLogins / distributionCycle
	newPercent := uint64(m.login.NewPercent)
	if cyclesPerSupercycle > math.MaxUint64/newPercent {
		return 0, errScheduleNewOrdinal
	}
	newPerSupercycle := cyclesPerSupercycle * newPercent

	laneNewCount := uint64(0)
	for lanePosition := uint64(0); lanePosition < lanePeriod; lanePosition++ {
		loginOrdinal := workerID + lanePosition*workerCount
		if ordinalPercent(loginOrdinal, m.loginPhase) < m.login.NewPercent {
			laneNewCount++
		}
	}
	if laneNewCount == 0 {
		return 0, errScheduleNewOrdinal
	}

	supercycle := localNewIndex / laneNewCount
	laneRank := localNewIndex % laneNewCount
	if supercycle > math.MaxUint64/newPerSupercycle {
		return 0, errScheduleNewOrdinal
	}
	ordinalBase := supercycle * newPerSupercycle
	for lanePosition := uint64(0); lanePosition < lanePeriod; lanePosition++ {
		loginOrdinal := workerID + lanePosition*workerCount
		if ordinalPercent(loginOrdinal, m.loginPhase) >= m.login.NewPercent {
			continue
		}
		if laneRank != 0 {
			laneRank--
			continue
		}
		withinSupercycle := m.NewOrdinalBefore(loginOrdinal)
		if ordinalBase > math.MaxUint64-withinSupercycle {
			return 0, errScheduleNewOrdinal
		}
		return ordinalBase + withinSupercycle, nil
	}
	return 0, errScheduleNewOrdinal
}

func (m ScheduleModel) loginOrdinalForNewOrdinal(globalNewOrdinal uint64) (uint64, error) {
	newPerCycle := uint64(m.login.NewPercent)
	cycle := globalNewOrdinal / newPerCycle
	rank := globalNewOrdinal % newPerCycle
	position := uint64(0)
	for ; position < distributionCycle; position++ {
		if ordinalPercent(position, m.loginPhase) >= m.login.NewPercent {
			continue
		}
		if rank == 0 {
			break
		}
		rank--
	}
	if position == distributionCycle || cycle > (math.MaxUint64-position)/distributionCycle {
		return 0, errScheduleNewOrdinal
	}
	return cycle*distributionCycle + position, nil
}

// LoginRates derives the reviewed identity-growth and total-login rates. For
// 250,000 new users/day at an 80% new share these are about 2.9/s and 3.6/s.
func (m ScheduleModel) LoginRates() LoginRates {
	newUsersPerSecond := float64(m.newUsersPerDay) / float64(secondsPerDay)
	return LoginRates{
		NewUsersPerSecond:    newUsersPerSecond,
		TotalLoginsPerSecond: newUsersPerSecond * 100 / float64(m.login.NewPercent),
	}
}

// Channel reconstructs one new relationship's initial burst and finite
// lifecycle activity. ownerIndex and peerIndex must use canonical order.
func (m ScheduleModel) Channel(relationshipOrdinal, ownerIndex, peerIndex uint64) (ChannelSchedule, error) {
	if ownerIndex >= peerIndex {
		return ChannelSchedule{}, errScheduleEndpointOrder
	}
	initialMessages, err := m.intInRange("initial-burst-message-count/v1", m.relationship.InitialMessages, relationshipOrdinal, ownerIndex, peerIndex)
	if err != nil {
		return ChannelSchedule{}, err
	}
	initialWindow, err := m.durationInRange("initial-burst-window/v1", m.relationship.InitialMessageWindow.Min, m.relationship.InitialMessageWindow.Max, relationshipOrdinal, ownerIndex, peerIndex)
	if err != nil {
		return ChannelSchedule{}, err
	}

	class := LifecycleClass(percentBucket(ordinalPercent(relationshipOrdinal, m.lifecyclePhase), m.lifecyclePercents[:]))
	schedule := ChannelSchedule{
		Class: class,
		InitialBurst: InitialBurstSchedule{
			MessageCount:        initialMessages,
			Window:              initialWindow,
			BothEndpointsOnline: true,
		},
		NaturalCooling: true,
	}
	switch class {
	case LifecycleRevisit:
		schedule.RevisitAfter, err = m.durationInRange("lifecycle-revisit-delay/v1", minimumRevisitDelay, maximumRevisitDelay, relationshipOrdinal, ownerIndex, peerIndex)
		if err != nil {
			return ChannelSchedule{}, err
		}
		schedule.RevisitMessages, err = m.intInRange("lifecycle-revisit-message-count/v1", m.relationship.ReturningMessages, relationshipOrdinal, ownerIndex, peerIndex)
		if err != nil {
			return ChannelSchedule{}, err
		}
		schedule.RequiresColdRuntimeEvidence = true
	case LifecycleRotating:
		schedule.ActiveFor, err = m.durationInRange("lifecycle-rotating-active-duration/v1", m.lifecycle.Rotating.ActiveDuration.Min, m.lifecycle.Rotating.ActiveDuration.Max, relationshipOrdinal, ownerIndex, peerIndex)
	case LifecycleLong:
		schedule.ActiveFor, err = m.durationInRange("lifecycle-long-active-duration/v1", m.lifecycle.Long.ActiveDuration.Min, m.lifecycle.Long.ActiveDuration.Max, relationshipOrdinal, ownerIndex, peerIndex)
	}
	if err != nil {
		return ChannelSchedule{}, err
	}
	return schedule, nil
}

func ordinalPercent(ordinal, phase uint64) int {
	return int((ordinal%distributionCycle + phase) % distributionCycle)
}

func percentBucket(percentile int, percents []int) int {
	cumulative := 0
	for bucket, percent := range percents {
		cumulative += percent
		if percentile < cumulative {
			return bucket
		}
	}
	return len(percents) - 1
}

func (m ScheduleModel) durationInRange(purpose string, minimum, maximum time.Duration, values ...uint64) (time.Duration, error) {
	span := uint64(maximum-minimum) + 1
	draw, err := m.identity.decisionBelow(purpose, span, values...)
	if err != nil {
		return 0, err
	}
	return minimum + time.Duration(draw), nil
}

func (m ScheduleModel) intInRange(purpose string, valueRange IntRange, values ...uint64) (int, error) {
	span := uint64(valueRange.Max) - uint64(valueRange.Min) + 1
	draw, err := m.identity.decisionBelow(purpose, span, values...)
	if err != nil {
		return 0, err
	}
	return valueRange.Min + int(draw), nil
}
