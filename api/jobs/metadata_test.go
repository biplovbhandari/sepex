package jobs

import (
	"strings"
	"testing"
)

func TestDigestFromRef(t *testing.T) {
	tests := []struct {
		imgURI string
		want   string
	}{
		{"repo/name@sha256:abc123", "sha256:abc123"},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/gdal@sha256:abc123", "sha256:abc123"},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/gdal:6", ""},
		{"nginx:latest", ""},
		{"nginx", ""},
		{"", ""},
	}

	for _, tt := range tests {
		if got := digestFromRef(tt.imgURI); got != tt.want {
			t.Errorf("digestFromRef(%q) = %q, want %q", tt.imgURI, got, tt.want)
		}
	}
}

func TestRegistryHost(t *testing.T) {
	tests := []struct {
		imgURI string
		want   string
	}{
		// No separator, or a first segment that is not hostname shaped, is
		// Docker Hub.
		{"postgres", "docker.io"},
		{"postgres:17.2-alpine3.20", "docker.io"},
		{"bitnami/postgres:17", "docker.io"},
		{"docker.io/library/postgres:17", "docker.io"},

		// A dot, a colon, or localhost in the first segment makes it a host.
		{"ghcr.io/osgeo/gdal:alpine-small-latest", "ghcr.io"},
		{"quay.io/prometheus/node-exporter:v1", "quay.io"},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/gdal:6", "123456789012.dkr.ecr.us-east-1.amazonaws.com"},
		{"localhost:5000/myimg:dev", "localhost:5000"},
		{"registry.internal:5000/team/img:dev", "registry.internal:5000"},
	}

	for _, tt := range tests {
		if got := registryHost(tt.imgURI); got != tt.want {
			t.Errorf("registryHost(%q) = %q, want %q", tt.imgURI, got, tt.want)
		}
	}
}

func TestResolveRegistryDigestRejectsUnsupportedRegistry(t *testing.T) {
	// A registry with no lookup implemented must be reported as such rather
	// than silently queried against Docker Hub.
	_, err := resolveRegistryDigest("quay.io/prometheus/node-exporter:v1")
	if err == nil {
		t.Fatal("resolveRegistryDigest accepted an unsupported registry, want an error")
	}
	if !strings.Contains(err.Error(), "quay.io") {
		t.Errorf("error %q does not name the offending registry", err)
	}
}

func TestParseECRImgURI(t *testing.T) {
	tests := []struct {
		name    string
		imgURI  string
		account string
		repo    string
		tag     string
		wantErr bool
	}{
		{
			name:    "plain repository and tag",
			imgURI:  "123456789012.dkr.ecr.us-east-1.amazonaws.com/gdal-ogrinfo:6",
			account: "123456789012",
			repo:    "gdal-ogrinfo",
			tag:     "6",
		},
		{
			name:    "namespaced repository keeps its slashes",
			imgURI:  "123456789012.dkr.ecr.us-east-1.amazonaws.com/team/gdal-ogrinfo:6",
			account: "123456789012",
			repo:    "team/gdal-ogrinfo",
			tag:     "6",
		},
		{
			name:    "digest pinned reference has no tag to resolve",
			imgURI:  "123456789012.dkr.ecr.us-east-1.amazonaws.com/gdal-ogrinfo@sha256:abc123",
			wantErr: true,
		},
		{
			name:    "missing tag",
			imgURI:  "123456789012.dkr.ecr.us-east-1.amazonaws.com/gdal-ogrinfo",
			wantErr: true,
		},
		{
			name:    "not a registry uri",
			imgURI:  "gdal-ogrinfo:6",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, repo, tag, err := parseECRImgURI(tt.imgURI)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseECRImgURI(%q) = (%q, %q, %q), want error", tt.imgURI, account, repo, tag)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseECRImgURI(%q) returned error: %v", tt.imgURI, err)
			}
			if account != tt.account || repo != tt.repo || tag != tt.tag {
				t.Errorf("parseECRImgURI(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.imgURI, account, repo, tag, tt.account, tt.repo, tt.tag)
			}
		})
	}
}
