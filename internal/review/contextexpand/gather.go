package contextexpand

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// gatherer accumulates candidate items across changed files while deduping
// symbols and caching parsed packages. It is single-goroutine (Expand drives
// it sequentially).
type gatherer struct {
	worktree string
	pkgCache map[string]*pkgIndex // package dir (abs) → parsed index
	seen     map[dedupKey]bool
	out      []Item
}

func newGatherer(worktree string) *gatherer {
	return &gatherer{
		worktree: worktree,
		pkgCache: map[string]*pkgIndex{},
		seen:     map[dedupKey]bool{},
	}
}

func (g *gatherer) items() []Item { return g.out }

// symbolRef locates a changed function definition (for the enrichers).
type symbolRef struct {
	Name string
	Path string // repo-relative, forward-slashed
	Line int    // definition line (name identifier)
	Col  int
}

// add appends an item unless the (path,name) symbol was already emitted. The
// dedup makes enclosing-function items win over callee/caller duplicates.
func (g *gatherer) add(it Item) bool {
	k := dedupKey{path: it.Path, name: it.Symbol}
	if it.Symbol != "" && g.seen[k] {
		return false
	}
	if it.Symbol != "" {
		g.seen[k] = true
	}
	g.out = append(g.out, it)
	return true
}

// gatherGoFile expands one changed Go file: the enclosing functions of its
// changed lines, the types those functions reference (same package), and the
// same-package functions they call. Returns the enclosing functions as
// symbolRefs so the caller can feed them to the cross-reference enrichers.
//
// Non-Go files and unparseable packages are skipped (fail-open, no error).
func (g *gatherer) gatherGoFile(cf ChangedFile) []symbolRef {
	path := strings.TrimSpace(cf.Path)
	if path == "" || !strings.HasSuffix(path, ".go") || len(cf.ChangedLines) == 0 {
		return nil
	}
	abs := filepath.Join(g.worktree, filepath.FromSlash(path))
	dir := filepath.Dir(abs)
	idx := g.loadPackage(dir)
	if idx == nil {
		return nil
	}
	pf := idx.fileByAbs(abs)
	if pf == nil {
		return nil
	}

	var refs []symbolRef
	// 1) Enclosing functions of the changed lines.
	encl := pf.enclosingFuncs(cf.ChangedLines)
	for _, fd := range encl {
		code := pf.nodeSource(declStart(fd), fd.End())
		if strings.TrimSpace(code) == "" {
			continue
		}
		namePos := idx.fset.Position(fd.Name.Pos())
		g.add(Item{
			Kind:   KindEnclosingFunc,
			Symbol: funcName(fd),
			Path:   path,
			Line:   idx.fset.Position(fd.Pos()).Line,
			Code:   code,
		})
		refs = append(refs, symbolRef{
			Name: fd.Name.Name,
			Path: path,
			Line: namePos.Line,
			Col:  namePos.Column,
		})
	}
	if len(encl) == 0 {
		return refs
	}

	// 2) Referenced type definitions + 3) same-package callees, collected from
	// the identifiers/calls used inside the enclosing functions.
	usedTypes, usedFuncs := collectUses(encl)
	// Deterministic order.
	for _, name := range sortedKeys(usedTypes) {
		if ref, ok := idx.typeByName[name]; ok {
			code := ref.file.nodeSource(ref.start, ref.end)
			if strings.TrimSpace(code) == "" {
				continue
			}
			g.add(Item{
				Kind:   KindTypeDef,
				Symbol: name,
				Path:   ref.file.rel,
				Line:   ref.line,
				Code:   code,
			})
		}
	}
	for _, name := range sortedKeys(usedFuncs) {
		if ref, ok := idx.funcByName[name]; ok {
			code := ref.file.nodeSource(ref.start, ref.end)
			if strings.TrimSpace(code) == "" {
				continue
			}
			g.add(Item{
				Kind:   KindCallee,
				Symbol: name,
				Path:   ref.file.rel,
				Line:   ref.line,
				Code:   code,
			})
		}
	}
	return refs
}

// gatherCallers asks the cross-reference finder (gopls → ctags) for locations
// that reference the changed functions and emits the enclosing function at
// each such location as a KindCaller item. Fully fail-open. Returns the names
// of enrichers that contributed at least one location.
func (g *gatherer) gatherCallers(ctx context.Context, timeout time.Duration, crossRef crossRefFunc, refs []symbolRef) []string {
	usedSet := map[string]bool{}
	for _, sym := range refs {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		found := crossRef(cctx, g.worktree, sym)
		cancel()
		for _, loc := range found.Locations {
			if strings.TrimSpace(loc.Path) == "" || loc.Line <= 0 {
				continue
			}
			g.emitCallerAt(loc)
		}
		if len(found.Locations) > 0 && found.Tool != "" {
			usedSet[found.Tool] = true
		}
	}
	var used []string
	for t := range usedSet {
		used = append(used, t)
	}
	sort.Strings(used)
	return used
}

// emitCallerAt resolves the enclosing function at a cross-reference location
// and emits it as a caller item. Skips non-Go / unparseable locations.
func (g *gatherer) emitCallerAt(loc Location) {
	if !strings.HasSuffix(loc.Path, ".go") {
		return
	}
	abs := filepath.Join(g.worktree, filepath.FromSlash(loc.Path))
	idx := g.loadPackage(filepath.Dir(abs))
	if idx == nil {
		return
	}
	pf := idx.fileByAbs(abs)
	if pf == nil {
		return
	}
	fd := pf.funcAtLine(loc.Line)
	if fd == nil {
		return
	}
	code := pf.nodeSource(declStart(fd), fd.End())
	if strings.TrimSpace(code) == "" {
		return
	}
	g.add(Item{
		Kind:   KindCaller,
		Symbol: funcName(fd),
		Path:   loc.Path,
		Line:   idx.fset.Position(fd.Pos()).Line,
		Code:   code,
	})
}

// loadPackage parses every non-test .go file in dir into one shared FileSet and
// indexes its top-level type/func declarations. Cached per dir. Returns nil on
// any I/O or (total) parse failure (fail-open).
func (g *gatherer) loadPackage(dir string) *pkgIndex {
	if idx, ok := g.pkgCache[dir]; ok {
		return idx
	}
	idx := parsePackageDir(g.worktree, dir)
	g.pkgCache[dir] = idx // cache even nil so we don't re-attempt
	return idx
}

// pkgIndex is a parsed package directory: its files plus name→decl maps for
// type and function resolution.
type pkgIndex struct {
	fset       *token.FileSet
	files      []*parsedFile
	byAbs      map[string]*parsedFile
	typeByName map[string]declRef
	funcByName map[string]declRef
}

func (idx *pkgIndex) fileByAbs(abs string) *parsedFile {
	if idx == nil {
		return nil
	}
	return idx.byAbs[filepath.Clean(abs)]
}

// parsedFile is one parsed source file with its bytes retained for slicing.
type parsedFile struct {
	abs  string
	rel  string // repo-relative, forward-slashed
	src  []byte
	ast  *ast.File
	fset *token.FileSet
}

// declRef points at a top-level declaration's source span.
type declRef struct {
	file  *parsedFile
	start token.Pos
	end   token.Pos
	line  int
}

// parsePackageDir reads and parses dir's Go files. Returns nil when the dir
// can't be read or no file parses.
func parsePackageDir(worktree, dir string) *pkgIndex {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	idx := &pkgIndex{
		fset:       fset,
		byAbs:      map[string]*parsedFile{},
		typeByName: map[string]declRef{},
		funcByName: map[string]declRef{},
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		// Skip generated files and tests — they add noise, not the definitions
		// the diff references.
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		abs := filepath.Join(dir, e.Name())
		src, rerr := os.ReadFile(abs)
		if rerr != nil {
			continue
		}
		f, perr := parser.ParseFile(fset, abs, src, parser.ParseComments|parser.SkipObjectResolution)
		if perr != nil || f == nil {
			// Fail-open: a single unparseable file is skipped; the rest of the
			// package still indexes.
			continue
		}
		pf := &parsedFile{
			abs:  filepath.Clean(abs),
			rel:  relSlash(worktree, abs),
			src:  src,
			ast:  f,
			fset: fset,
		}
		idx.files = append(idx.files, pf)
		idx.byAbs[pf.abs] = pf
		indexDecls(idx, pf)
	}
	if len(idx.files) == 0 {
		return nil
	}
	return idx
}

// indexDecls records top-level type and function declarations of pf into idx.
// The first declaration of a name wins (deterministic since files are read in
// directory order).
func indexDecls(idx *pkgIndex, pf *parsedFile) {
	for _, d := range pf.ast.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			name := decl.Name.Name
			if _, exists := idx.funcByName[name]; exists {
				continue
			}
			idx.funcByName[name] = declRef{
				file:  pf,
				start: declStart(decl),
				end:   decl.End(),
				line:  pf.fset.Position(decl.Pos()).Line,
			}
		case *ast.GenDecl:
			if decl.Tok != token.TYPE {
				continue
			}
			for _, spec := range decl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				name := ts.Name.Name
				if _, exists := idx.typeByName[name]; exists {
					continue
				}
				// Prefer the whole GenDecl span (captures the doc comment and,
				// for a lone-spec `type X struct{…}`, the `type` keyword).
				start, end := typeSpecSpan(decl, ts)
				idx.typeByName[name] = declRef{
					file:  pf,
					start: start,
					end:   end,
					line:  pf.fset.Position(ts.Pos()).Line,
				}
			}
		}
	}
}

// typeSpecSpan returns the source span to render for a type. When the GenDecl
// holds exactly one spec, the whole GenDecl (with `type` keyword + doc) is
// used; otherwise just the single spec (so grouped `type (...)` blocks don't
// dump every sibling type).
func typeSpecSpan(gd *ast.GenDecl, ts *ast.TypeSpec) (token.Pos, token.Pos) {
	if len(gd.Specs) == 1 {
		start := gd.Pos()
		if gd.Doc != nil {
			start = gd.Doc.Pos()
		}
		return start, gd.End()
	}
	start := ts.Pos()
	if ts.Doc != nil {
		start = ts.Doc.Pos()
	}
	return start, ts.End()
}

// enclosingFuncs returns the top-level FuncDecls of pf whose body span covers
// at least one of the changed lines. Order follows declaration order.
func (pf *parsedFile) enclosingFuncs(changedLines []int) []*ast.FuncDecl {
	if len(changedLines) == 0 {
		return nil
	}
	var out []*ast.FuncDecl
	for _, d := range pf.ast.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start := pf.fset.Position(fd.Pos()).Line
		end := pf.fset.Position(fd.End()).Line
		for _, ln := range changedLines {
			if ln >= start && ln <= end {
				out = append(out, fd)
				break
			}
		}
	}
	return out
}

// funcAtLine returns the top-level FuncDecl whose span covers line, or nil.
func (pf *parsedFile) funcAtLine(line int) *ast.FuncDecl {
	for _, d := range pf.ast.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start := pf.fset.Position(fd.Pos()).Line
		end := pf.fset.Position(fd.End()).Line
		if line >= start && line <= end {
			return fd
		}
	}
	return nil
}

// nodeSource slices the original source between two positions (inclusive of
// start, exclusive of end). Returns "" when the span is out of range.
func (pf *parsedFile) nodeSource(start, end token.Pos) string {
	lo := pf.fset.Position(start).Offset
	hi := pf.fset.Position(end).Offset
	if lo < 0 || hi > len(pf.src) || lo >= hi {
		return ""
	}
	return string(pf.src[lo:hi])
}

// collectUses walks the enclosing functions and returns the set of identifier
// names used as (candidate) type references and as called functions. It is a
// deliberately syntactic (not type-checked) heuristic: it collects every
// capitalised-or-not identifier that appears in a type position or a call
// position and lets the caller intersect with the package's declared
// type/func names. That intersection is what makes it precise without a full
// type checker.
func collectUses(funcs []*ast.FuncDecl) (types map[string]bool, calls map[string]bool) {
	types = map[string]bool{}
	calls = map[string]bool{}
	addType := func(expr ast.Expr) {
		for _, n := range typeIdents(expr) {
			types[n] = true
		}
	}
	for _, fd := range funcs {
		// Signature types (receiver, params, results) reference types too.
		if fd.Recv != nil {
			for _, f := range fd.Recv.List {
				addType(f.Type)
			}
		}
		if fd.Type != nil {
			if fd.Type.Params != nil {
				for _, f := range fd.Type.Params.List {
					addType(f.Type)
				}
			}
			if fd.Type.Results != nil {
				for _, f := range fd.Type.Results.List {
					addType(f.Type)
				}
			}
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				switch fn := x.Fun.(type) {
				case *ast.Ident:
					calls[fn.Name] = true
				case *ast.SelectorExpr:
					// pkg.Func or recv.Method — record the method/func name;
					// same-package method resolution is best-effort by name.
					calls[fn.Sel.Name] = true
				}
			case *ast.CompositeLit:
				if x.Type != nil {
					addType(x.Type)
				}
			case *ast.ValueSpec:
				if x.Type != nil {
					addType(x.Type)
				}
			case *ast.TypeAssertExpr:
				if x.Type != nil {
					addType(x.Type)
				}
			case *ast.Field:
				if x.Type != nil {
					addType(x.Type)
				}
			}
			return true
		})
	}
	return types, calls
}

// typeIdents extracts the bare type-name identifiers from a type expression,
// unwrapping pointers/slices/maps/arrays and taking the base of a qualified
// selector (pkg.T → skipped, since it is not same-package). Returns the
// same-package candidate names.
func typeIdents(expr ast.Expr) []string {
	var out []string
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch t := e.(type) {
		case *ast.Ident:
			out = append(out, t.Name)
		case *ast.StarExpr:
			walk(t.X)
		case *ast.ArrayType:
			walk(t.Elt)
		case *ast.MapType:
			walk(t.Key)
			walk(t.Value)
		case *ast.ChanType:
			walk(t.Value)
		case *ast.Ellipsis:
			walk(t.Elt)
		case *ast.SelectorExpr:
			// pkg.Type — cross-package, not resolvable in this package index.
		}
	}
	walk(expr)
	return out
}

// declStart returns the position a declaration's rendered source should begin
// at — its doc comment when present, else the decl keyword.
func declStart(fd *ast.FuncDecl) token.Pos {
	if fd.Doc != nil {
		return fd.Doc.Pos()
	}
	return fd.Pos()
}

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return recvTypeName(fd.Recv.List[0].Type) + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver Foo[T]
		return recvTypeName(t.X)
	default:
		return ""
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// relSlash returns abs relative to worktree with forward slashes; falls back to
// the cleaned base name when abs is outside worktree.
func relSlash(worktree, abs string) string {
	rel, err := filepath.Rel(worktree, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}
