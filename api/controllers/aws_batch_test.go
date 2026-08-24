package controllers

import "testing"

func TestClusterFromTaskARN(t *testing.T) {
	tests := []struct {
		name    string
		arn     string
		cluster string
		ok      bool
	}{
		{
			name:    "long form arn carries the cluster",
			arn:     "arn:aws:ecs:us-east-1:123456789012:task/sepex-cluster/1abc2d34ef567890",
			cluster: "sepex-cluster",
			ok:      true,
		},
		{
			name: "short form arn predates the cluster segment",
			arn:  "arn:aws:ecs:us-east-1:123456789012:task/1abc2d34ef567890",
			ok:   false,
		},
		{
			name: "not a task arn",
			arn:  "arn:aws:batch:us-east-1:123456789012:job/1abc2d34ef567890",
			ok:   false,
		},
		{
			name: "empty",
			arn:  "",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster, ok := clusterFromTaskARN(tt.arn)
			if ok != tt.ok {
				t.Fatalf("clusterFromTaskARN(%q) ok = %v, want %v", tt.arn, ok, tt.ok)
			}
			if cluster != tt.cluster {
				t.Errorf("clusterFromTaskARN(%q) = %q, want %q", tt.arn, cluster, tt.cluster)
			}
		})
	}
}
