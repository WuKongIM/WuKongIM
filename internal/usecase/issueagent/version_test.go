package issueagent_test

import (
	"context"
	"errors"
	"testing"

	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

const (
	affectedSHA = "0123456789abcdef0123456789abcdef01234567"
	baseSHA     = "1234567890abcdef1234567890abcdef12345678"
	otherSHA    = "234567890abcdef1234567890abcdef123456789"
)

type fakeSourceResolver struct {
	commits map[string]bool
	tags    map[string][]string
	images  map[string]issueagentusecase.ImageSource
	err     error
}

func (fake fakeSourceResolver) CommitExists(_ context.Context, sha string) (bool, error) {
	if fake.err != nil {
		return false, fake.err
	}
	return fake.commits[sha], nil
}

func (fake fakeSourceResolver) ResolveTag(_ context.Context, tag string) ([]string, error) {
	if fake.err != nil {
		return nil, fake.err
	}
	return fake.tags[tag], nil
}

func (fake fakeSourceResolver) ResolveImageDigest(
	_ context.Context,
	image string,
) (issueagentusecase.ImageSource, error) {
	if fake.err != nil {
		return issueagentusecase.ImageSource{}, fake.err
	}
	return fake.images[image], nil
}

func TestResolveVersionsPinsCommitReleaseAndVerifiedImage(t *testing.T) {
	t.Parallel()

	image := "ghcr.io/wukongim/wukongim@sha256:" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	resolver := fakeSourceResolver{
		commits: map[string]bool{affectedSHA: true, baseSHA: true},
		tags:    map[string][]string{"v2.1.0": {affectedSHA, affectedSHA}},
		images: map[string]issueagentusecase.ImageSource{
			image: {SourceSHA: affectedSHA, Verified: true},
		},
	}
	for _, reported := range []string{affectedSHA, "v2.1.0", image} {
		versions, err := issueagentusecase.ResolveVersions(
			context.Background(), resolver, reported, baseSHA,
		)
		require.NoError(t, err)
		require.Equal(t, affectedSHA, versions.AffectedSHA)
		require.Equal(t, baseSHA, versions.DiagnosisBaseSHA)
		require.Nil(t, versions.IntegrationBase)
	}
}

func TestResolveVersionsRejectsMovingMissingAmbiguousAndUnverifiedRefs(t *testing.T) {
	t.Parallel()

	image := "wk@sha256:" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	resolver := fakeSourceResolver{
		commits: map[string]bool{baseSHA: true},
		tags:    map[string][]string{"v2.1.0": {affectedSHA, otherSHA}},
		images: map[string]issueagentusecase.ImageSource{
			image: {SourceSHA: affectedSHA, Verified: false},
		},
	}
	for _, reported := range []string{"latest", "main", affectedSHA, "v2.1.0", image} {
		_, err := issueagentusecase.ResolveVersions(
			context.Background(), resolver, reported, baseSHA,
		)
		require.Error(t, err, reported)
	}
	resolver.err = errors.New("backend secret")
	_, err := issueagentusecase.ResolveVersions(
		context.Background(), resolver, affectedSHA, baseSHA,
	)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "backend secret")
}

func TestAffectedVersionForAuthorizationDefaultsToExactMainSHA(t *testing.T) {
	t.Parallel()

	reported, err := issueagentusecase.AffectedVersionForAuthorization("", baseSHA)
	require.NoError(t, err)
	require.Equal(t, baseSHA, reported)

	reported, err = issueagentusecase.AffectedVersionForAuthorization(
		"v2.1.0", baseSHA,
	)
	require.NoError(t, err)
	require.Equal(t, "v2.1.0", reported)

	for _, input := range []struct {
		reported string
		mainSHA  string
	}{
		{reported: "latest", mainSHA: baseSHA},
		{reported: "main", mainSHA: baseSHA},
		{reported: "", mainSHA: "not-a-sha"},
	} {
		_, err := issueagentusecase.AffectedVersionForAuthorization(
			input.reported, input.mainSHA,
		)
		require.Error(t, err)
	}
}

var _ issueagentusecase.SourceResolver = fakeSourceResolver{}
