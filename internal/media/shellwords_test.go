package media

import (
	"reflect"
	"testing"
)

func TestSplitShellWordsMatchesSingleFileDefaults(t *testing.T) {
	got, err := splitShellWords(`'--browser-arg="--headless=new"' '--browser-arg="--user-data-dir=./chromium-profile"' '--browser-arg="--no-sandbox"' '--browser-arg="--load-extension=uBOLite.chromium.mv3"'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`--browser-arg="--headless=new"`,
		`--browser-arg="--user-data-dir=./chromium-profile"`,
		`--browser-arg="--no-sandbox"`,
		`--browser-arg="--load-extension=uBOLite.chromium.mv3"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	got, err = splitShellWords(`--example="a b" 'c d' "" e\ f`)
	if err != nil || !reflect.DeepEqual(got, []string{"--example=a b", "c d", "", "e f"}) {
		t.Fatalf("custom words: %q %v", got, err)
	}
	if _, err := splitShellWords(`"unfinished`); err == nil {
		t.Fatal("unmatched quote accepted")
	}
}
