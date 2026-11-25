package scancache

import (
	"encoding/binary"
	"errors"

	"github.com/qxbao/bgraph/internal/model"
)

// Compact binary encoding for ScanResult (measured: smaller and faster to decode than JSON, see the benchmark in store_test.go).
//
// Layout: [ver=1][flags: bit0 uncertain][uvarint len+bytes UncertainReason][uvarint import count]
// per import: [kind byte][flags: bit0 dynamic][uvarint line][str spec][str suffix]; str = uvarint len + bytes.
// Kind is an index into the kinds table; 255 = an unknown string follows (forward compatibility when new kinds are added).
//
// Optional trailer for Rust results (absent in results without a Rust section, so older blobs still decode):
// [1][uvarint mod count] per mod: [str name][uvarint line][str path][str unknown][uvarint nest count][str nest...];
// [uvarint include count] per include: [str macro][str kind][str path][uvarint line].
// Optional trailer for Go results (after the Rust one if both were ever present): [2][str package][uvarint embed count] per embed:
// [str pattern][uvarint line][str unknown]; [uvarint string count][str...].

const codecVersion = 1

var kinds = []model.ImportKind{
	model.ImportStatic, model.ImportTypeOnly, model.ImportDynamic, model.ImportRequire, model.ImportResolve,
	model.ImportMock, model.ImportGlob, model.ImportURL, model.ImportReExport, model.ImportTripleSlash,
	model.ImportSysPath, model.ImportPlugin, model.ImportRead,
	// Appended (never reorder: stored rows refer to kinds by index).
	model.ImportRbRequire, model.ImportRbLoad, model.ImportRbGlob, model.ImportRbDataGlob, model.ImportRbString, model.ImportRbZeitwerk,
	model.ImportRbPushDir, model.ImportRbRails, model.ImportRbTestPattern, model.ImportRbTestTask, model.ImportRbRubyOpt,
	model.ImportRbTestLib, model.ImportRbGem, model.ImportRbGemspec, model.ImportRbBundler, model.ImportRbBinPath,
	model.ImportRbFixturesAll, model.ImportRbLoadPath,
	model.ImportDartImport, model.ImportDartExport, model.ImportDartPart, model.ImportDartString, model.ImportDartLoad,
	model.ImportDartProcess,
}

var kindIdx = func() map[model.ImportKind]byte {
	m := map[model.ImportKind]byte{}
	for i, k := range kinds {
		m[k] = byte(i)
	}
	return m
}()

func appendStr(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func encode(r model.ScanResult) []byte {
	b := make([]byte, 0, 16+len(r.Imports)*24)
	b = append(b, codecVersion)
	var fl byte
	if r.Uncertain {
		fl = 1
	}
	b = append(b, fl)
	b = appendStr(b, r.UncertainReason)
	b = binary.AppendUvarint(b, uint64(len(r.Imports)))
	for _, im := range r.Imports {
		if i, ok := kindIdx[im.Kind]; ok {
			b = append(b, i)
		} else {
			b = append(b, 255)
			b = appendStr(b, string(im.Kind))
		}
		var f byte
		if im.Dynamic {
			f = 1
		}
		b = append(b, f)
		b = binary.AppendUvarint(b, uint64(im.Line))
		b = appendStr(b, im.Spec)
		b = appendStr(b, im.Suffix)
	}
	if rs := r.Rust; rs != nil {
		b = append(b, 1)
		b = binary.AppendUvarint(b, uint64(len(rs.Mods)))
		for _, m := range rs.Mods {
			b = appendStr(b, m.Name)
			b = binary.AppendUvarint(b, uint64(m.Line))
			b = appendStr(b, m.Path)
			b = appendStr(b, m.Unknown)
			b = binary.AppendUvarint(b, uint64(len(m.Nest)))
			for _, n := range m.Nest {
				b = appendStr(b, n)
			}
		}
		b = binary.AppendUvarint(b, uint64(len(rs.Includes)))
		for _, inc := range rs.Includes {
			b = appendStr(b, inc.Macro)
			b = appendStr(b, inc.Kind)
			b = appendStr(b, inc.Path)
			b = binary.AppendUvarint(b, uint64(inc.Line))
		}
	}
	if g := r.Go; g != nil {
		b = append(b, 2)
		b = appendStr(b, g.Package)
		b = binary.AppendUvarint(b, uint64(len(g.Embeds)))
		for _, e := range g.Embeds {
			b = appendStr(b, e.Pattern)
			b = binary.AppendUvarint(b, uint64(e.Line))
			b = appendStr(b, e.Unknown)
		}
		b = binary.AppendUvarint(b, uint64(len(g.Strings)))
		for _, s := range g.Strings {
			b = appendStr(b, s)
		}
	}
	if d := r.Dart; d != nil {
		b = append(b, 3)
		b = appendStrings(b, d.Words)
		b = appendStrings(b, d.Standalone)
		b = binary.AppendUvarint(b, uint64(len(d.Exports)))
		for _, ex := range d.Exports {
			b = appendStr(b, ex.URI)
			var hide byte
			if ex.Hide {
				hide = 1
			}
			b = append(b, hide)
			b = appendStrings(b, ex.Names)
		}
	}
	return b
}

func appendStrings(b []byte, values []string) []byte {
	b = binary.AppendUvarint(b, uint64(len(values)))
	for _, s := range values {
		b = appendStr(b, s)
	}
	return b
}

var errCodec = errors.New("scancache: corrupt encoded data")

type reader struct {
	b   []byte
	err error
}

func (r *reader) byte() byte {
	if len(r.b) == 0 {
		r.err = errCodec
		return 0
	}
	c := r.b[0]
	r.b = r.b[1:]
	return c
}

func (r *reader) uvarint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = errCodec
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) str() string {
	n := r.uvarint()
	if r.err != nil || n > uint64(len(r.b)) {
		r.err = errCodec
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

func (r *reader) strs() []string {
	n := r.uvarint()
	if r.err != nil || n > uint64(len(r.b)) {
		r.err = errCodec
		return nil
	}
	var out []string
	for i := uint64(0); i < n && r.err == nil; i++ {
		out = append(out, r.str())
	}
	return out
}

func decode(b []byte) (model.ScanResult, error) {
	r := &reader{b: b}
	var out model.ScanResult
	if r.byte() != codecVersion {
		return out, errCodec
	}
	out.Uncertain = r.byte()&1 != 0
	out.UncertainReason = r.str()
	n := r.uvarint()
	if r.err != nil || n > uint64(len(b)) { // each import takes ≥ 5 bytes, so n > len means corruption
		return model.ScanResult{}, errCodec
	}
	if n > 0 {
		out.Imports = make([]model.ImportRef, 0, n)
	}
	for i := uint64(0); i < n; i++ {
		var im model.ImportRef
		k := r.byte()
		if k == 255 {
			im.Kind = model.ImportKind(r.str())
		} else if int(k) < len(kinds) {
			im.Kind = kinds[k]
		} else {
			return model.ScanResult{}, errCodec
		}
		im.Dynamic = r.byte()&1 != 0
		im.Line = int(r.uvarint())
		im.Spec = r.str()
		im.Suffix = r.str()
		if r.err != nil {
			return model.ScanResult{}, r.err
		}
		out.Imports = append(out.Imports, im)
	}
	for len(r.b) > 0 {
		var err error
		switch tag := r.byte(); {
		case tag == 1 && out.Rust == nil && out.Go == nil:
			out.Rust, err = decodeRust(r)
		case tag == 2 && out.Go == nil:
			out.Go, err = decodeGo(r)
		case tag == 3 && out.Dart == nil:
			out.Dart, err = decodeDart(r)
		default:
			err = errCodec
		}
		if err != nil {
			return model.ScanResult{}, err
		}
	}
	return out, nil
}

func decodeDart(r *reader) (*model.DartScan, error) {
	d := &model.DartScan{Words: r.strs(), Standalone: r.strs()}
	n := r.uvarint()
	if r.err != nil || n > uint64(len(r.b)) {
		return nil, errCodec
	}
	for i := uint64(0); i < n && r.err == nil; i++ {
		d.Exports = append(d.Exports, model.DartExport{URI: r.str(), Hide: r.byte() != 0, Names: r.strs()})
	}
	return d, r.err
}

func decodeRust(r *reader) (*model.RustScan, error) {
	rs := &model.RustScan{}
	nm := r.uvarint()
	if r.err != nil || nm > uint64(len(r.b)) {
		return nil, errCodec
	}
	for i := uint64(0); i < nm; i++ {
		m := model.RustMod{Name: r.str()}
		m.Line = int(r.uvarint())
		m.Path = r.str()
		m.Unknown = r.str()
		nn := r.uvarint()
		if r.err != nil || nn > uint64(len(r.b)) {
			return nil, errCodec
		}
		for j := uint64(0); j < nn; j++ {
			m.Nest = append(m.Nest, r.str())
		}
		if r.err != nil {
			return nil, r.err
		}
		rs.Mods = append(rs.Mods, m)
	}
	ni := r.uvarint()
	if r.err != nil || ni > uint64(len(r.b)) {
		return nil, errCodec
	}
	for i := uint64(0); i < ni; i++ {
		inc := model.RustInclude{Macro: r.str(), Kind: r.str(), Path: r.str()}
		inc.Line = int(r.uvarint())
		if r.err != nil {
			return nil, r.err
		}
		rs.Includes = append(rs.Includes, inc)
	}
	return rs, nil
}

func decodeGo(r *reader) (*model.GoScan, error) {
	g := &model.GoScan{Package: r.str()}
	ne := r.uvarint()
	if r.err != nil || ne > uint64(len(r.b)) {
		return nil, errCodec
	}
	for i := uint64(0); i < ne; i++ {
		e := model.GoEmbed{Pattern: r.str()}
		e.Line = int(r.uvarint())
		e.Unknown = r.str()
		if r.err != nil {
			return nil, r.err
		}
		g.Embeds = append(g.Embeds, e)
	}
	ns := r.uvarint()
	if r.err != nil || ns > uint64(len(r.b)) {
		return nil, errCodec
	}
	for i := uint64(0); i < ns; i++ {
		g.Strings = append(g.Strings, r.str())
		if r.err != nil {
			return nil, r.err
		}
	}
	return g, nil
}
