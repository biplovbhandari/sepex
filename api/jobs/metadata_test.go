package jobs

import "testing"

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
