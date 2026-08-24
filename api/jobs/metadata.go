package jobs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
)

type process struct {
	ProcessID      string `json:"processId"`
	ProcessVersion string `json:"processVersion"`
}

// How a recorded image digest was obtained, strongest first.
// These are documented for users in the Metadata section of the README, and
// declared as a term in context.jsonld. Keep all three in sync.
const (
	// DigestSourcePinned means the digest was already part of the image
	// reference, so nothing had to be resolved.
	DigestSourcePinned = "pinned"
	// DigestSourceObserved means the executor reported the digest it actually
	// pulled: the Docker daemon locally, or ECS for AWS Batch.
	DigestSourceObserved = "observed"
	// DigestSourceTagLookup means we asked the registry what a mutable tag
	// pointed at. Done while the job was running, so it is almost certainly
	// what ran, but it is inference rather than observation.
	DigestSourceTagLookup = "tag-lookup"
	// DigestSourceUnavailable means no digest could be determined.
	DigestSourceUnavailable = "unavailable"
)

type image struct {
	ImageURI     string `json:"imageURI"`
	ImageDigest  string `json:"imageDigest,omitempty"`
	DigestSource string `json:"digestSource,omitempty"`
}

// digestFromRef returns the digest embedded in a pinned image reference
// (repo@sha256:...), or an empty string when the reference is not pinned.
func digestFromRef(imgURI string) string {
	if i := strings.LastIndex(imgURI, "@"); i != -1 {
		return imgURI[i+1:]
	}
	return ""
}

// Define a metaData object
type metaData struct {
	Context string `json:"@context"`
	JobID   string `json:"apiJobId"`
	// User    string  `json:"apiUser"`
	Process process `json:"process"`
	Image   image   `json:"image,omitempty"`
	// RecoveryNotice indicates the job was recovered after restart and metadata may be incomplete.
	RecoveryNotice string `json:"recoveryNotice,omitempty"`
	// ComputeEnvironmentURI    string    // ARN
	// ComputeEnvironmentDigest string    // required for reproducibility, will need to be custom implemented
	Commands        []string  `json:"commands"`
	GeneratedAtTime time.Time `json:"generatedAtTime"` // not implemented
	StartedAtTime   time.Time `json:"startedAtTime"`   // not implemented
	EndedAtTime     time.Time `json:"endedAtTime"`
}

// resolveRegistryDigest asks the image's registry what a tag currently points
// at. It is only accurate insofar as the tag has not moved since the workload
// pulled it, so callers must record the result as DigestSourceRegistry rather
// than as the digest that certainly ran.
func resolveRegistryDigest(imgURI string) (string, error) {
	switch {
	case strings.Contains(imgURI, "amazonaws.com/"):
		return getECRImageDigest(imgURI)
	case strings.Contains(imgURI, "ghcr.io/"):
		return getGHCRImageDigest(imgURI, "")
	default:
		return getDkrHubImageDigest(imgURI, "dummy")
	}
}

// Get image digest from ecr
func getECRImageDigest(imgURI string) (string, error) {
	var imgDgst string

	sess, err := session.NewSession()
	if err != nil {
		return "", err
	}
	ecrClient := ecr.New(sess)

	accountID, repositoryName, imgTag, err := parseECRImgURI(imgURI)
	if err != nil {
		return "", err
	}

	// Retrieve the image details from ECR
	describeImagesInput := &ecr.DescribeImagesInput{
		RegistryId:     aws.String(accountID),
		RepositoryName: aws.String(repositoryName),
		ImageIds: []*ecr.ImageIdentifier{
			{
				ImageTag: aws.String(imgTag),
			},
		},
	}

	describeImagesOutput, err := ecrClient.DescribeImages(describeImagesInput)
	if err != nil {
		return "", err
	}

	// Get the digest from the image details
	if len(describeImagesOutput.ImageDetails) > 0 {
		imgDgst = aws.StringValue(describeImagesOutput.ImageDetails[0].ImageDigest)
	} else {
		return "", fmt.Errorf("image not found in ECR")
	}

	return imgDgst, nil
}

// Helper function to parse the ECR Image URI
func parseECRImgURI(imgURI string) (string, string, string, error) {
	// A digest pinned reference carries its digest already, so callers must
	// read it with digestFromRef rather than asking the registry to resolve a
	// tag that is not there.
	if digestFromRef(imgURI) != "" {
		return "", "", "", fmt.Errorf("reference is digest pinned, there is no tag to resolve: %s", imgURI)
	}

	// Split the repository URI into registry host, repository name, and image
	// tag. The repository name may itself contain slashes, so only the first
	// separator delimits the host.
	registry, repoWithTag, found := strings.Cut(imgURI, "/")
	if !found {
		return "", "", "", fmt.Errorf("invalid repository URI: %s", imgURI)
	}

	accountID := strings.Split(registry, ".")[0]

	// Tags cannot contain a colon, so the last one separates name from tag.
	i := strings.LastIndex(repoWithTag, ":")
	if i == -1 {
		return "", "", "", fmt.Errorf("invalid image tag in repository URI: %s", imgURI)
	}

	repositoryName := repoWithTag[:i]
	imageTag := repoWithTag[i+1:]

	if repositoryName == "" || imageTag == "" {
		return "", "", "", fmt.Errorf("invalid image tag in repository URI: %s", imgURI)
	}

	return accountID, repositoryName, imageTag, nil
}

// Get Image Digest from Docker Hub
// arch based digest not yet implemented, arch is not used
func getDkrHubImageDigest(imgURI string, arch string) (string, error) {
	if digestFromRef(imgURI) != "" {
		return "", fmt.Errorf("reference is digest pinned, there is no tag to resolve: %s", imgURI)
	}

	parts := strings.Split(imgURI, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid image Name: %s", imgURI)
	}

	imageName := parts[0]
	imageTag := parts[1]

	url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/tags/%s/images", imageName, imageTag)

	client := http.Client{
		Timeout: 10 * time.Second, // Set a timeout for the request
	}

	response, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("error sending request: %s", err)

	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %s", err)
	}

	var result []interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", fmt.Errorf("error parsing JSON: %s", err)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("no images returned for %s", imgURI)
	}

	// Currently it gets just the first image, while there can be more than 1. This is incorrect
	digest, ok := result[0].(map[string]interface{})["digest"].(string)
	if !ok {
		return "", fmt.Errorf("error retrieving image digest")
	}

	return digest, nil
}

// getGHCRImageDigest fetches the canonical image digest from GHCR for any tag.
// If token is empty, it fetches an anonymous token for public repositories.
// GHCR requires authentication even for public images.
func getGHCRImageDigest(imgURI, token string) (string, error) {
	if digestFromRef(imgURI) != "" {
		return "", fmt.Errorf("reference is digest pinned, there is no tag to resolve: %s", imgURI)
	}

	// Split repository and tag
	parts := strings.Split(imgURI, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid image name: %s", imgURI)
	}
	repo := strings.TrimPrefix(parts[0], "ghcr.io/")
	tag := parts[1]

	// Fetch anonymous token if not provided
	if token == "" {
		var err error
		token, err = fetchGHCRToken(repo)
		if err != nil {
			return "", fmt.Errorf("error fetching token: %w", err)
		}
	}

	url := fmt.Sprintf("https://ghcr.io/v2/%s/manifests/%s", repo, tag)

	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	// Accept headers to cover single-arch and multi-arch, Docker & OCI
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
	}, ","))

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", fmt.Errorf("tag %s not found in repo %s", tag, repo)
	} else if resp.StatusCode >= 400 {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Digest is always in this header
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("digest header missing")
	}

	return digest, nil
}

// To do: see if GHCR has rate limits for anonymous token requests
// fetchGHCRToken fetches a public pull token for GHCR
func fetchGHCRToken(repo string) (string, error) {
	url := fmt.Sprintf("https://ghcr.io/token?service=ghcr.io&scope=repository:%s:pull", repo)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("error fetching token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading token response: %w", err)
	}

	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("error parsing token JSON: %w", err)
	}

	if data.Token == "" {
		return "", fmt.Errorf("token missing in response")
	}

	return data.Token, nil
}
