package langagents

// NamingConvention captures the expected identifier casing for a
// language's three most-common identifier kinds plus the doc-comment
// shape used by tooling in that language.
//
// Empty strings mean "no strong default" — callers (e.g. the deterministic
// convention gate) short-circuit those into no-ops, so we never strip a
// finding on uncertain ground.
type NamingConvention struct {
	// Func is the casing convention for function and method names.
	// Examples: "MixedCaps" (Go), "snake_case" (Python, Rust),
	// "camelCase" (TypeScript, Java, Kotlin).
	Func string
	// Type is the casing for class/struct/interface/enum names.
	// Almost always "PascalCase" except in Go where everything exported
	// is MixedCaps (i.e. PascalCase under the hood, but the language
	// calls it MixedCaps).
	Type string
	// Var is the casing for variable, field, and constant names. Same
	// rules of thumb as Func for most languages.
	Var string
}

// ForKind returns the convention for an identifier kind name ("function",
// "type", "variable"). Unknown kinds fall back to Func then Type — most
// real-world model recommendations talk about function or method casing.
func (c NamingConvention) ForKind(kind string) string {
	switch kind {
	case "function":
		return c.Func
	case "type":
		return c.Type
	case "variable":
		return c.Var
	}
	if c.Func != "" {
		return c.Func
	}
	return c.Type
}

// Table is the canonical, single-source naming-convention table for every
// language that ships with a bundled brief. It is consulted by:
//
//  1. convention_gate.go to decide whether a model's "should be in
//     <case>" claim conflicts with the file's language.
//  2. The bundled language briefs (via Naming(language) returning the
//     same data in markdown form) so the prompt and the gate cannot
//     drift apart.
//  3. Tests (table_test.go) that load each bundled brief and verify the
//     "Naming" section's bullet points spell the same conventions this
//     table records.
//
// Languages without a Table entry are silently skipped by the gate (the
// LLM-generated brief, if any, still flows into the prompt).
var Table = map[Language]NamingConvention{
	"go":         {Func: "MixedCaps", Type: "MixedCaps", Var: "MixedCaps"},
	"python":     {Func: "snake_case", Type: "PascalCase", Var: "snake_case"},
	"rust":       {Func: "snake_case", Type: "PascalCase", Var: "snake_case"},
	"typescript": {Func: "camelCase", Type: "PascalCase", Var: "camelCase"},
	"java":       {Func: "camelCase", Type: "PascalCase", Var: "camelCase"},
	"kotlin":     {Func: "camelCase", Type: "PascalCase", Var: "camelCase"},
}

// LabelFor returns a short human display label for a canonical language
// ("Go", "Python", "TypeScript", ...). Used by reasons strings, the TUI,
// and progress events. Unknown languages return the input string as-is so
// the label always renders something.
func LabelFor(lang Language) string {
	switch lang {
	case "go":
		return "Go"
	case "python":
		return "Python"
	case "typescript":
		return "TypeScript"
	case "rust":
		return "Rust"
	case "hcl":
		return "HCL"
	case "java":
		return "Java"
	case "kotlin":
		return "Kotlin"
	case "ruby":
		return "Ruby"
	case "swift":
		return "Swift"
	case "c":
		return "C"
	case "cpp":
		return "C++"
	case "csharp":
		return "C#"
	case "shell":
		return "Shell"
	case "sql":
		return "SQL"
	case "yaml":
		return "YAML"
	case "json":
		return "JSON"
	case "markdown":
		return "Markdown"
	}
	if lang == "" {
		return ""
	}
	return lang
}
