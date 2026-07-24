package backup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	"golang.org/x/sys/unix"
)

const (
	defaultBackupClockSkewLimit = 2 * time.Minute
	backupClockProbeTimeout     = 10 * time.Second
)

// RepositoryDoctor verifies one immutable repository's production controls.
type RepositoryDoctor interface {
	Check(context.Context) error
}

// KMSDoctor verifies backup encryption and signing keys.
type KMSDoctor interface {
	Check(context.Context, string, string) error
}

// ClockProbe returns authoritative remote UTC evidence.
type ClockProbe interface {
	UTC(context.Context) (time.Time, error)
}

// DoctorOptions configures fail-closed backup-only readiness checks.
type DoctorOptions struct {
	Primary         RepositoryDoctor
	Secondary       RepositoryDoctor
	KMS             KMSDoctor
	EncryptionKey   string
	SigningKey      string
	StagingDir      string
	ApplicationDir  string
	StagingMaxBytes uint64
	ClockProbes     []ClockProbe
	Now             func() time.Time
	MaxClockSkew    time.Duration
}

// Doctor validates external durability controls without affecting message readiness.
type Doctor struct {
	options DoctorOptions
}

// NewDoctor creates a backup startup doctor.
func NewDoctor(options DoctorOptions) (*Doctor, error) {
	if options.Primary == nil || options.Secondary == nil || options.KMS == nil || strings.TrimSpace(options.EncryptionKey) == "" || strings.TrimSpace(options.SigningKey) == "" || !filepath.IsAbs(options.StagingDir) || options.StagingMaxBytes == 0 || len(options.ClockProbes) == 0 {
		return nil, fmt.Errorf("backup doctor: incomplete options")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MaxClockSkew == 0 {
		options.MaxClockSkew = defaultBackupClockSkewLimit
	}
	if options.MaxClockSkew < 0 {
		return nil, fmt.Errorf("backup doctor: clock skew limit must be positive")
	}
	return &Doctor{options: options}, nil
}

// Check proves repositories, keys, staging capacity, and UTC evidence.
func (d *Doctor) Check(ctx context.Context) (backupcontract.DoctorReport, error) {
	if d == nil {
		return backupcontract.DoctorReport{}, fmt.Errorf("backup doctor: unavailable")
	}
	checks := []struct {
		name   string
		fn     func() error
		assign func(backupcontract.Health)
	}{
		{name: "primary_repository", fn: func() error { return d.options.Primary.Check(ctx) }},
		{name: "secondary_repository", fn: func() error { return d.options.Secondary.Check(ctx) }},
		{name: "kms", fn: func() error { return d.options.KMS.Check(ctx, d.options.EncryptionKey, d.options.SigningKey) }},
		{name: "staging", fn: func() error {
			return checkBackupStaging(d.options.StagingDir, d.options.ApplicationDir, d.options.StagingMaxBytes)
		}},
		{name: "utc", fn: func() error { return d.checkUTC(ctx) }},
	}
	report := backupcontract.DoctorReport{CheckedAtUnixMillis: d.options.Now().UTC().UnixMilli()}
	checks[0].assign = func(health backupcontract.Health) { report.Primary = health }
	checks[1].assign = func(health backupcontract.Health) { report.Secondary = health }
	checks[2].assign = func(health backupcontract.Health) { report.KMS = health }
	checks[3].assign = func(health backupcontract.Health) { report.Staging = health }
	checks[4].assign = func(health backupcontract.Health) { report.UTC = health }
	var firstErr error
	for _, check := range checks {
		if err := check.fn(); err != nil {
			check.assign(backupcontract.HealthFailed)
			if firstErr == nil {
				report.FailureCategory = check.name
				firstErr = fmt.Errorf("backup doctor %s: %w", check.name, err)
			}
			continue
		}
		check.assign(backupcontract.HealthHealthy)
	}
	return report, firstErr
}

func (d *Doctor) checkUTC(ctx context.Context) error {
	now := d.options.Now().UTC()
	for _, probe := range d.options.ClockProbes {
		remote, err := probe.UTC(ctx)
		if err != nil {
			return err
		}
		skew := now.Sub(remote.UTC())
		if skew < 0 {
			skew = -skew
		}
		if skew > d.options.MaxClockSkew {
			return fmt.Errorf("UTC skew %s exceeds %s", skew.Round(time.Second), d.options.MaxClockSkew)
		}
	}
	return nil
}

func checkBackupStaging(stagingDir, applicationDir string, requiredBytes uint64) error {
	cleanStaging := filepath.Clean(stagingDir)
	if applicationDir != "" {
		cleanApplication := filepath.Clean(applicationDir)
		if cleanStaging == cleanApplication || pathWithin(cleanStaging, cleanApplication) || pathWithin(cleanApplication, cleanStaging) {
			return fmt.Errorf("staging and application data directories must not overlap")
		}
	}
	if err := os.MkdirAll(cleanStaging, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(cleanStaging)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("staging path must be a real directory")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(cleanStaging, &stat); err != nil {
		return err
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if available < requiredBytes {
		return fmt.Errorf("staging capacity %d is below required %d", available, requiredBytes)
	}
	probe, err := os.CreateTemp(cleanStaging, ".doctor-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	defer os.Remove(name)
	if _, err := probe.Write([]byte("wukongim-backup-doctor-v1")); err != nil {
		_ = probe.Close()
		return err
	}
	if err := probe.Sync(); err != nil {
		_ = probe.Close()
		return err
	}
	return probe.Close()
}

func pathWithin(candidate, parent string) bool {
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// EndpointClockProbe reads the authenticated transport peer's HTTP Date header.
// A 2xx status is not required because S3-compatible endpoints commonly return
// Date on an unauthenticated HEAD while still denying the operation.
type EndpointClockProbe struct {
	endpoint string
	client   *http.Client
}

// NewEndpointClockProbe creates a bounded HTTPS UTC probe.
func NewEndpointClockProbe(endpoint string, client *http.Client) (*EndpointClockProbe, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("backup clock probe: HTTPS endpoint is required")
	}
	if client == nil {
		client = &http.Client{Timeout: backupClockProbeTimeout}
	}
	return &EndpointClockProbe{endpoint: parsed.String(), client: client}, nil
}

// UTC returns the endpoint Date header as UTC evidence.
func (p *EndpointClockProbe) UTC(ctx context.Context) (time.Time, error) {
	if p == nil || p.client == nil {
		return time.Time{}, fmt.Errorf("backup clock probe: unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, p.endpoint, nil)
	if err != nil {
		return time.Time{}, err
	}
	response, err := p.client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer response.Body.Close()
	value := strings.TrimSpace(response.Header.Get("Date"))
	if value == "" {
		return time.Time{}, errors.New("remote Date header is missing")
	}
	remote, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid remote Date header: %w", err)
	}
	return remote.UTC(), nil
}
