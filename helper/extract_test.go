package helper

import "testing"

func TestExtractName(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "no slash", input: "cluster", want: "cluster"},
		{name: "arn", input: "arn:aws:ecs:eu-west-1:111111111111:cluster/my-cluster", want: "my-cluster"},
		{name: "trailing slash", input: "prefix/", want: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractName(tc.input)
			if got != tc.want {
				t.Fatalf("ExtractName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractImageName(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "simple image", input: "nginx", want: "nginx"},
		{name: "with path and tag", input: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/repo/app:1.2.3", want: "app:1.2.3"},
		{name: "with digest", input: "registry.local/repo/app@sha256:abcd", want: "app@sha256:abcd"},
		{name: "trailing slash", input: "registry/repo/", want: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractImageName(tc.input)
			if got != tc.want {
				t.Fatalf("ExtractImageName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
