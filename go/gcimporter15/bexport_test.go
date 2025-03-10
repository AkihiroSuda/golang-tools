// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build go1.6

package gcimporter_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"runtime"
	"testing"

	"golang.org/x/tools/go/buildutil"
	gcimporter "golang.org/x/tools/go/gcimporter15"
	"golang.org/x/tools/go/loader"
)

func TestBExportData_stdlib(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skipf("incomplete std lib on %s", runtime.GOOS)
	}

	// Load, parse and type-check the program.
	ctxt := build.Default // copy
	ctxt.GOPATH = ""      // disable GOPATH
	conf := loader.Config{
		Build:       &ctxt,
		AllowErrors: true,
	}
	for _, path := range buildutil.AllPackages(conf.Build) {
		conf.Import(path)
	}

	// Create a package containing type and value errors to ensure
	// they are properly encoded/decoded.
	f, err := conf.ParseFile("haserrors/haserrors.go", `package haserrors
const UnknownValue = "" + 0
type UnknownType undefined
`)
	if err != nil {
		t.Fatal(err)
	}
	conf.CreateFromFiles("haserrors", f)

	prog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	numPkgs := len(prog.AllPackages)
	if want := 248; numPkgs < want {
		t.Errorf("Loaded only %d packages, want at least %d", numPkgs, want)
	}

	for pkg, info := range prog.AllPackages {
		if info.Files == nil {
			continue // empty directory
		}
		exportdata := gcimporter.BExportData(pkg)

		imports := make(map[string]*types.Package)
		n, pkg2, err := gcimporter.BImportData(imports, exportdata, pkg.Path())
		if err != nil {
			t.Errorf("BImportData(%s): %v", pkg.Path(), err)
			continue
		}
		if n != len(exportdata) {
			t.Errorf("BImportData(%s) decoded %d bytes, want %d",
				pkg.Path(), n, len(exportdata))
		}

		// Compare the packages' corresponding members.
		for _, name := range pkg.Scope().Names() {
			if !ast.IsExported(name) {
				continue
			}
			obj1 := pkg.Scope().Lookup(name)
			obj2 := pkg2.Scope().Lookup(name)
			if obj2 == nil {
				t.Errorf("%s.%s not found, want %s", pkg.Path(), name, obj1)
				continue
			}
			if err := equalObj(obj1, obj2); err != nil {
				t.Errorf("%s.%s: %s\ngot:  %s\nwant: %s",
					pkg.Path(), name, err, obj2, obj1)
			}
		}
	}
}

// equalObj reports how x and y differ.  They are assumed to belong to
// different universes so cannot be compared directly.
func equalObj(x, y types.Object) error {
	if reflect.TypeOf(x) != reflect.TypeOf(y) {
		return fmt.Errorf("%T vs %T", x, y)
	}
	xt := x.Type()
	yt := y.Type()
	switch x.(type) {
	case *types.Var, *types.Func:
		// ok
	case *types.Const:
		xval := x.(*types.Const).Val()
		yval := y.(*types.Const).Val()
		// Use string comparison for floating-point values since rounding is permitted.
		if constant.Compare(xval, token.NEQ, yval) &&
			!(xval.Kind() == constant.Float && xval.String() == yval.String()) {
			return fmt.Errorf("unequal constants %s vs %s", xval, yval)
		}
	case *types.TypeName:
		xt = xt.Underlying()
		yt = yt.Underlying()
	default:
		return fmt.Errorf("unexpected %T", x)
	}
	return equalType(xt, yt)
}

func equalType(x, y types.Type) error {
	if reflect.TypeOf(x) != reflect.TypeOf(y) {
		return fmt.Errorf("unequal kinds: %T vs %T", x, y)
	}
	switch x := x.(type) {
	case *types.Interface:
		y := y.(*types.Interface)
		// TODO(gri): enable separate emission of Embedded interfaces
		// and ExplicitMethods then use this logic.
		// if x.NumEmbeddeds() != y.NumEmbeddeds() {
		// 	return fmt.Errorf("unequal number of embedded interfaces: %d vs %d",
		// 		x.NumEmbeddeds(), y.NumEmbeddeds())
		// }
		// for i := 0; i < x.NumEmbeddeds(); i++ {
		// 	xi := x.Embedded(i)
		// 	yi := y.Embedded(i)
		// 	if xi.String() != yi.String() {
		// 		return fmt.Errorf("mismatched %th embedded interface: %s vs %s",
		// 			i, xi, yi)
		// 	}
		// }
		// if x.NumExplicitMethods() != y.NumExplicitMethods() {
		// 	return fmt.Errorf("unequal methods: %d vs %d",
		// 		x.NumExplicitMethods(), y.NumExplicitMethods())
		// }
		// for i := 0; i < x.NumExplicitMethods(); i++ {
		// 	xm := x.ExplicitMethod(i)
		// 	ym := y.ExplicitMethod(i)
		// 	if xm.Name() != ym.Name() {
		// 		return fmt.Errorf("mismatched %th method: %s vs %s", i, xm, ym)
		// 	}
		// 	if err := equalType(xm.Type(), ym.Type()); err != nil {
		// 		return fmt.Errorf("mismatched %s method: %s", xm.Name(), err)
		// 	}
		// }
		if x.NumMethods() != y.NumMethods() {
			return fmt.Errorf("unequal methods: %d vs %d",
				x.NumMethods(), y.NumMethods())
		}
		for i := 0; i < x.NumMethods(); i++ {
			xm := x.Method(i)
			ym := y.Method(i)
			if xm.Name() != ym.Name() {
				return fmt.Errorf("mismatched %th method: %s vs %s", i, xm, ym)
			}
			if err := equalType(xm.Type(), ym.Type()); err != nil {
				return fmt.Errorf("mismatched %s method: %s", xm.Name(), err)
			}
		}
	case *types.Array:
		y := y.(*types.Array)
		if x.Len() != y.Len() {
			return fmt.Errorf("unequal array lengths: %d vs %d", x, y)
		}
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("array elements: %s", err)
		}
	case *types.Basic:
		y := y.(*types.Basic)
		if x.Kind() != y.Kind() {
			return fmt.Errorf("unequal basic types: %s vs %s", x, y)
		}
	case *types.Chan:
		y := y.(*types.Chan)
		if x.Dir() != y.Dir() {
			return fmt.Errorf("unequal channel directions: %s vs %s", x.Dir(), y.Dir())
		}
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("channel elements: %s", err)
		}
	case *types.Map:
		y := y.(*types.Map)
		if err := equalType(x.Key(), y.Key()); err != nil {
			return fmt.Errorf("map keys: %s", err)
		}
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("map values: %s", err)
		}
	case *types.Named:
		y := y.(*types.Named)
		if x.String() != y.String() {
			return fmt.Errorf("unequal named types: %s vs %s", x, y)
		}
	case *types.Pointer:
		y := y.(*types.Pointer)
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("pointer elements: %s", err)
		}
	case *types.Signature:
		y := y.(*types.Signature)
		if err := equalType(x.Params(), y.Params()); err != nil {
			return fmt.Errorf("parameters: %s", err)
		}
		if err := equalType(x.Results(), y.Results()); err != nil {
			return fmt.Errorf("results: %s", err)
		}
		if x.Variadic() != y.Variadic() {
			return fmt.Errorf("unequal varidicity: %t vs %t",
				x.Variadic(), y.Variadic())
		}
		if (x.Recv() != nil) != (y.Recv() != nil) {
			return fmt.Errorf("unequal receivers: %s vs %s", x.Recv(), y.Recv())
		}
		if x.Recv() != nil {
			// TODO(adonovan): fix: this assertion fires for interface methods.
			// The type of the receiver of an interface method is a named type
			// if the Package was loaded from export data, or an unnamed (interface)
			// type if the Package was produced by type-checking ASTs.
			// if err := equalType(x.Recv().Type(), y.Recv().Type()); err != nil {
			// 	return fmt.Errorf("receiver: %s", err)
			// }
		}
	case *types.Slice:
		y := y.(*types.Slice)
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("slice elements: %s", err)
		}
	case *types.Struct:
		y := y.(*types.Struct)
		if x.NumFields() != y.NumFields() {
			return fmt.Errorf("unequal struct fields: %d vs %d",
				x.NumFields(), y.NumFields())
		}
		for i := 0; i < x.NumFields(); i++ {
			xf := x.Field(i)
			yf := y.Field(i)
			if xf.Name() != yf.Name() {
				return fmt.Errorf("mismatched fields: %s vs %s", xf, yf)
			}
			if err := equalType(xf.Type(), yf.Type()); err != nil {
				return fmt.Errorf("struct field %s: %s", xf.Name(), err)
			}
			if x.Tag(i) != y.Tag(i) {
				return fmt.Errorf("struct field %s has unequal tags: %q vs %q",
					xf.Name(), x.Tag(i), y.Tag(i))
			}
		}
	case *types.Tuple:
		y := y.(*types.Tuple)
		if x.Len() != y.Len() {
			return fmt.Errorf("unequal tuple lengths: %d vs %d", x, y)
		}
		for i := 0; i < x.Len(); i++ {
			if err := equalType(x.At(i).Type(), y.At(i).Type()); err != nil {
				return fmt.Errorf("tuple element %d: %s", i, err)
			}
		}
	}
	return nil
}
