package main

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"
)

func TestNewHaystack(t *testing.T) {
	tests := []struct {
		input []string
		want  *haystack
	}{
		{input: []string{"hello", "world"}, want: &haystack{
			search: []byte("helloworld"),
			spans: []span{
				{start: 0, end: 5},
				{start: 5, end: 10},
			},
		}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.input), func(t *testing.T) {
			got := NewHaystack(tt.input)
			if !reflect.DeepEqual(tt.want, got) {
				t.Errorf("want [%+v], got [%+v]", tt.want, got)
			}
		})
	}
}

func TestHaystackFindAll(t *testing.T) {
	tests := []struct {
		domain []string
		needle string
		want   map[string]struct{}
	}{
		{
			domain: []string{"hello", "world"},
			needle: "wor",
			want: map[string]struct{}{
				"world": {},
			},
		},
		{
			domain: []string{"The Official Go Blog", "Kubernetes Feed"},
			needle: "go",
			want: map[string]struct{}{
				"The Official Go Blog": {},
			},
		},
		{
			domain: []string{"The Official Go Blog", "Go Weekly"},
			needle: "go",
			want: map[string]struct{}{
				"The Official Go Blog": {},
				"Go Weekly":            {},
			},
		},
		{
			domain: []string{"The Official Go Blog", "Go Weekly"},
			needle: "o",
			want: map[string]struct{}{
				"The Official Go Blog": {},
				"Go Weekly":            {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("find %s in %v", tt.needle, tt.domain), func(t *testing.T) {
			h := NewHaystack(tt.domain)
			gotList := h.FindAll(regexp.MustCompile(tt.needle))
			got := make(map[string]struct{})
			for _, g := range gotList {
				got[g] = struct{}{}
			}
			if !reflect.DeepEqual(tt.want, got) {
				t.Errorf("want %v, got %v", tt.want, got)
			}
		})
	}
}
