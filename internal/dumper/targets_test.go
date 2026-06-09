package dumper

import (
	"reflect"
	"testing"
)

func TestResolveComponents(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		want      []string
		wantErr   bool
	}{
		{name: "empty returns all", requested: nil, want: AllComponents},
		{
			name:      "subset is reordered canonically",
			requested: []string{"kubelet", "kube-apiserver"},
			want:      []string{"kube-apiserver", "kubelet"},
		},
		{name: "unknown errors", requested: []string{"etcd"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveComponents(tt.requested)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterPages(t *testing.T) {
	target := []string{"flagz", "statusz", "configz"}
	tests := []struct {
		name      string
		requested []string
		want      []string
	}{
		{name: "empty keeps all", requested: nil, want: target},
		{name: "intersection preserves target order", requested: []string{"configz", "flagz"}, want: []string{"flagz", "configz"}},
		{name: "no overlap is empty", requested: []string{"healthz"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterPages(target, tt.requested); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
