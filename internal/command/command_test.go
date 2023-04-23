package command

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []CommandRequest
	}{
		{
			name:  "not-array",
			input: `[{"talk":"blah"}]`,
			want:  []CommandRequest{{Name: "talk", Args: []any{"blah"}}},
		},
		{
			name:  "separated-arrays",
			input: `[{"talk":"blah"}] [{"talk":"blah"}] [{"talk":"blah"}]`,
			want: []CommandRequest{
				{Name: "talk", Args: []any{"blah"}},
				{Name: "talk", Args: []any{"blah"}},
				{Name: "talk", Args: []any{"blah"}},
			},
		},
		{
			name:  "write",
			input: `[{"write": ["file.txt", "hello world"]}]`,
			want: []CommandRequest{
				{Name: "write", Args: []any{"file.txt", "hello world"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse() = %v, want %v", got, tt.want)
			}
		})
	}
}
