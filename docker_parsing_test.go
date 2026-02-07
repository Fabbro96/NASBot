package main

import (
	"reflect"
	"testing"
)

func TestParseDockerJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []ContainerInfo
		wantErr bool
	}{
		{
			name:  "Single Running Container",
			input: `{"Names":"my-container","Status":"Up 2 hours","State":"running","Image":"my-image","ID":"12345"}`,
			want: []ContainerInfo{
				{Name: "my-container", Status: "Up 2 hours", Image: "my-image", ID: "12345", Running: true},
			},
			wantErr: false,
		},
		{
			name: "Multiple Containers (Mixed Status)",
			input: `{"Names":"c1","Status":"Up 1 hour","State":"running","Image":"img1","ID":"111"}
{"Names":"c2","Status":"Exited (0)","State":"exited","Image":"img2","ID":"222"}`,
			want: []ContainerInfo{
				{Name: "c1", Status: "Up 1 hour", Image: "img1", ID: "111", Running: true},
				{Name: "c2", Status: "Exited (0)", Image: "img2", ID: "222", Running: false},
			},
			wantErr: false,
		},
		{
			name:    "Empty Input",
			input:   "",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "Whitespace Only",
			input:   "   \n  ",
			want:    nil,
			wantErr: false,
		},
		{
			name: "Malformed JSON (Should Skip)",
			input: `{"Names":"ok","State":"running"}
BAD_JSON_LINE
{"Names":"ok2","State":"running"}`,
			// Expecting it to parse the valid lines and skip the bad one without failing the whole batch
			// (Behavior defined in docker.go implementation: log warning and continue)
			want: []ContainerInfo{
				{Name: "ok", Status: "", Image: "", ID: "", Running: true},
				{Name: "ok2", Status: "", Image: "", ID: "", Running: true},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDockerJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDockerJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseDockerJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}
