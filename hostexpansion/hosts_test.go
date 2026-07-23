package hostexpansion

import (
	"reflect"
	"testing"
)

type Tests struct {
	name   string
	input  string
	output []string
}

func Test_HostExpansion(t *testing.T) {
	tests := []Tests{
		{
			name:  "single-bracket",
			input: "app-[1..4].domain1.tld",
			output: []string{
				"app-1.domain1.tld",
				"app-2.domain1.tld",
				"app-3.domain1.tld",
				"app-4.domain1.tld",
			},
		},
		{
			name:  "error-syntax",
			input: "app-].domain1.tld",
			output: []string{
				"invalid-host:app-].domain1.tld",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := ExpandHosts(tt.input)
			if !reflect.DeepEqual(results, tt.output) {
				t.Errorf("Expected %v but got %v", tt.output, results)
			}
		})
	}
}
