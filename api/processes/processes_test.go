package processes

import "testing"

func TestJobDefIsPinned(t *testing.T) {
	tests := []struct {
		jobDef string
		want   bool
	}{
		{"gdal-ogrinfo:6", true},
		{"process-sandbox:2", true},
		{"arn:aws:batch:us-east-1:123456789012:job-definition/gdal-ogrinfo:6", true},

		{"gdal-ogrinfo", false},
		{"arn:aws:batch:us-east-1:123456789012:job-definition/gdal-ogrinfo", false},
		{"gdal-ogrinfo:", false},
		{"gdal-ogrinfo:latest", false},
		{"gdal-ogrinfo:6a", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := jobDefIsPinned(tt.jobDef); got != tt.want {
			t.Errorf("jobDefIsPinned(%q) = %v, want %v", tt.jobDef, got, tt.want)
		}
	}
}
