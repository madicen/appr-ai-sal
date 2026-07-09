package staticpass

import "testing"

func TestParseGofmtList(t *testing.T) {
	out := "internal/a/x.go\ninternal/b/y.go\n\n"
	anns := parseGofmtList(out)
	if len(anns) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(anns), anns)
	}
	if anns[0].Path != "internal/a/x.go" || anns[0].Tool != "gofmt" || anns[0].Level != LevelWarning {
		t.Fatalf("unexpected annotation: %+v", anns[0])
	}
	if len(parseGofmtList("")) != 0 {
		t.Fatalf("empty output should yield no annotations")
	}
}

func TestParseGoVet(t *testing.T) {
	stderr := "# github.com/x/y\n" +
		"./foo.go:12:3: Printf format %d has arg s of wrong type string\n" +
		"vet: some build noise\n" +
		"bar.go:7: unreachable code\n"
	anns := parseGoVet(stderr)
	if len(anns) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(anns), anns)
	}
	if anns[0].Path != "foo.go" || anns[0].Line != 12 || anns[0].Level != LevelError {
		t.Fatalf("unexpected: %+v", anns[0])
	}
	if anns[1].Path != "bar.go" || anns[1].Line != 7 {
		t.Fatalf("unexpected: %+v", anns[1])
	}
}

func TestParseGolangciJSON(t *testing.T) {
	b := []byte(`{"Issues":[
	  {"FromLinter":"errcheck","Text":"Error return value not checked","Severity":"error","Pos":{"Filename":"a.go","Line":42}},
	  {"FromLinter":"gofmt","Text":"File is not gofmt-ed","Severity":"warning","Pos":{"Filename":"b.go","Line":1}}
	]}`)
	anns, err := parseGolangciJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 2 {
		t.Fatalf("want 2, got %d", len(anns))
	}
	if anns[0].Path != "a.go" || anns[0].Line != 42 || anns[0].Level != LevelError {
		t.Fatalf("unexpected: %+v", anns[0])
	}
	if anns[0].Message == "" || anns[0].Tool != "golangci-lint" {
		t.Fatalf("unexpected: %+v", anns[0])
	}
	if got, _ := parseGolangciJSON(nil); got != nil {
		t.Fatalf("empty input should be nil")
	}
	if _, err := parseGolangciJSON([]byte("not json")); err == nil {
		t.Fatalf("expected parse error on garbage")
	}
}

func TestParseRuffJSON(t *testing.T) {
	b := []byte(`[{"code":"F401","message":"imported but unused","filename":"m.py","location":{"row":3}}]`)
	anns, err := parseRuffJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 || anns[0].Path != "m.py" || anns[0].Line != 3 {
		t.Fatalf("unexpected: %+v", anns)
	}
	if anns[0].Message != "F401: imported but unused" {
		t.Fatalf("message not composed: %q", anns[0].Message)
	}
}

func TestParseEslintJSON(t *testing.T) {
	b := []byte(`[{"filePath":"a.ts","messages":[
	  {"line":10,"message":"Missing semicolon","severity":1,"ruleId":"semi"},
	  {"line":20,"message":"x is not defined","severity":2,"ruleId":"no-undef"}
	]}]`)
	anns, err := parseEslintJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 2 {
		t.Fatalf("want 2, got %d", len(anns))
	}
	if anns[0].Level != LevelWarning || anns[1].Level != LevelError {
		t.Fatalf("severity mapping wrong: %+v", anns)
	}
	if anns[0].Path != "a.ts" {
		t.Fatalf("path wrong: %+v", anns[0])
	}
}

func TestParseTerraformValidateJSON(t *testing.T) {
	b := []byte(`{"valid":false,"diagnostics":[
	  {"severity":"error","summary":"Unsupported argument","detail":"An argument named \"tags\" is not expected here.","range":{"filename":"main.tf","start":{"line":5}}}
	]}`)
	anns, err := parseTerraformValidateJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 {
		t.Fatalf("want 1, got %d", len(anns))
	}
	a := anns[0]
	if a.Path != "main.tf" || a.Line != 5 || a.Level != LevelError {
		t.Fatalf("unexpected: %+v", a)
	}
	if a.Message == "" || a.Tool != "terraform validate" {
		t.Fatalf("unexpected: %+v", a)
	}
}

func TestNormalizeLevel(t *testing.T) {
	cases := map[string]Level{"error": LevelError, "WARNING": LevelWarning, "info": LevelNotice, "weird": LevelWarning}
	for in, want := range cases {
		if got := normalizeLevel(in, LevelWarning); got != want {
			t.Fatalf("normalizeLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilesWithExtAndUniqueDirs(t *testing.T) {
	files := []string{"a/x.go", "a/y.go", "b/z.py", "c.go"}
	goFiles := filesWithExt(files, ".go")
	if len(goFiles) != 3 {
		t.Fatalf("want 3 go files, got %v", goFiles)
	}
	dirs := uniqueDirs(goFiles)
	// a, ., (c.go is at root)
	if len(dirs) != 2 || dirs[0] != "." || dirs[1] != "a" {
		t.Fatalf("unexpected dirs: %v", dirs)
	}
}
