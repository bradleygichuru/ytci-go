package server

import (
	"reflect"
	"testing"
)

func TestSplitOrigins(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  []string{},
		},
		{
			name:  "wildcard",
			input: "*",
			want:  []string{"*"},
		},
		{
			name:  "single origin",
			input: "http://localhost:3000",
			want:  []string{"http://localhost:3000"},
		},
		{
			name:  "multiple origins",
			input: "http://localhost:3000,https://admin.example.com",
			want:  []string{"http://localhost:3000", "https://admin.example.com"},
		},
		{
			name:  "whitespace around origins",
			input: " http://localhost:3000 , https://admin.example.com ",
			want:  []string{"http://localhost:3000", "https://admin.example.com"},
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  []string{},
		},
		{
			name:  "commas with empty parts",
			input: "http://localhost:3000,,https://admin.example.com",
			want:  []string{"http://localhost:3000", "https://admin.example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitOrigins(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitOrigins(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
