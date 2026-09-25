package search

import (
	"reflect"
	"testing"
)

func TestParsePrecedenceAndQuotedText(t *testing.T) {
	got, err := Parse(`alpha or #Go not "two words" and !unread`)
	if err != nil {
		t.Fatal(err)
	}
	want := Or{Term{"alpha"}, And{And{Tag{"Go"}, Not{Term{"two words"}}}, Keyword{"unread"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	got, err = Parse(`'say \'hello\'' #tag`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, And{Term{"say 'hello'"}, Tag{"tag"}}) {
		t.Fatalf("quoted escape: %#v", got)
	}
}

func TestParseMalformedAndEmptyQueries(t *testing.T) {
	for _, query := range []string{"and", "alpha or", "(alpha", "alpha )", "not"} {
		if _, err := Parse(query); err == nil {
			t.Errorf("%q unexpectedly parsed", query)
		}
	}
	for _, query := range []string{"", " # ! "} {
		got, err := Parse(query)
		if err != nil || got != nil {
			t.Errorf("%q: %#v, %v", query, got, err)
		}
	}
	got, err := Parse(`"unfinished`)
	if err != nil || !reflect.DeepEqual(got, Term{"unfinished"}) {
		t.Errorf("unterminated quote: %#v, %v", got, err)
	}
}
