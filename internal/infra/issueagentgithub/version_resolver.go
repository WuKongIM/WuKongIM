package issueagentgithub

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

const maxAnnotatedTagDepth = 4

// ImageSourceLookup verifies source metadata for one immutable image digest.
// Registry-specific authentication remains outside the GitHub client.
type ImageSourceLookup func(
	context.Context,
	string,
) (issueagentusecase.ImageSource, error)

// VersionSourceResolver adapts GitHub Git-object reads and an injected image
// metadata verifier to the provider-neutral version-pinning port.
type VersionSourceResolver struct {
	client      *Client
	imageSource ImageSourceLookup
}

// NewVersionSourceResolver constructs a read-only immutable-source resolver.
func NewVersionSourceResolver(
	client *Client,
	imageSource ImageSourceLookup,
) (*VersionSourceResolver, error) {
	if client == nil || imageSource == nil {
		return nil, errors.New("version source resolver dependencies are missing")
	}
	return &VersionSourceResolver{client: client, imageSource: imageSource}, nil
}

// CommitExists verifies one exact commit object without accepting a branch ref.
func (resolver *VersionSourceResolver) CommitExists(
	ctx context.Context,
	sha string,
) (bool, error) {
	if resolver == nil || !gitObjectPattern.MatchString(sha) || len(sha) != 40 {
		return false, errors.New("commit identity is invalid")
	}
	var response struct {
		SHA string `json:"sha"`
	}
	err := resolver.client.requestJSON(
		ctx, http.MethodGet,
		"/repos/"+resolver.client.repository+"/git/commits/"+sha,
		nil, &response, http.StatusOK, http.StatusNotFound,
	)
	if err != nil {
		return false, err
	}
	if response.SHA == "" {
		return false, nil
	}
	if response.SHA != sha {
		return false, errors.New("GitHub commit response identity mismatch")
	}
	return true, nil
}

// ResolveTag dereferences a lightweight or annotated release tag to one commit.
func (resolver *VersionSourceResolver) ResolveTag(
	ctx context.Context,
	tag string,
) ([]string, error) {
	if resolver == nil || !issueagentusecase.IsReleaseTagSyntax(tag) {
		return nil, errors.New("release tag syntax is invalid")
	}
	var reference struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	err := resolver.client.requestJSON(
		ctx, http.MethodGet,
		"/repos/"+resolver.client.repository+"/git/ref/tags/"+url.PathEscape(tag),
		nil, &reference, http.StatusOK, http.StatusNotFound,
	)
	if err != nil {
		return nil, err
	}
	if reference.Ref == "" {
		return nil, nil
	}
	if reference.Ref != "refs/tags/"+tag {
		return nil, errors.New("GitHub tag response identity mismatch")
	}
	objectType, objectSHA := reference.Object.Type, reference.Object.SHA
	seen := make(map[string]struct{}, maxAnnotatedTagDepth)
	for depth := 0; depth < maxAnnotatedTagDepth; depth++ {
		if !gitObjectPattern.MatchString(objectSHA) || len(objectSHA) != 40 {
			return nil, errors.New("GitHub tag points to an invalid object")
		}
		switch objectType {
		case "commit":
			return []string{objectSHA}, nil
		case "tag":
			if _, duplicate := seen[objectSHA]; duplicate {
				return nil, errors.New("annotated tag chain contains a cycle")
			}
			seen[objectSHA] = struct{}{}
			var tagObject struct {
				SHA    string `json:"sha"`
				Object struct {
					Type string `json:"type"`
					SHA  string `json:"sha"`
				} `json:"object"`
			}
			if err := resolver.client.getJSON(
				ctx,
				"/repos/"+resolver.client.repository+"/git/tags/"+objectSHA,
				&tagObject,
			); err != nil {
				return nil, err
			}
			if tagObject.SHA != objectSHA {
				return nil, errors.New("annotated tag object identity mismatch")
			}
			objectType, objectSHA = tagObject.Object.Type, tagObject.Object.SHA
		default:
			return nil, errors.New("release tag does not resolve to a commit")
		}
	}
	return nil, errors.New("annotated tag chain exceeds depth limit")
}

// ResolveImageDigest delegates only immutable image references to the injected
// registry metadata verifier.
func (resolver *VersionSourceResolver) ResolveImageDigest(
	ctx context.Context,
	image string,
) (issueagentusecase.ImageSource, error) {
	if resolver == nil || !issueagentusecase.IsImageDigestSyntax(image) {
		return issueagentusecase.ImageSource{}, errors.New("image digest syntax is invalid")
	}
	return resolver.imageSource(ctx, image)
}

var _ issueagentusecase.SourceResolver = (*VersionSourceResolver)(nil)
