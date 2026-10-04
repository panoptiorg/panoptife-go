package app

import "os/exec"

// Codec is a WIDE interface (12 impls > fan-out cap 10): its call sites must
// stay opaque. Codec07 hides an exec sink that must never surface — if a
// capped site leaked targets, the exec chain would appear.
type Codec interface {
	Encode(s string) string
}

type Codec01 struct{}

func (Codec01) Encode(s string) string { return s + "01" }

type Codec02 struct{}

func (Codec02) Encode(s string) string { return s + "02" }

type Codec03 struct{}

func (Codec03) Encode(s string) string { return s + "03" }

type Codec04 struct{}

func (Codec04) Encode(s string) string { return s + "04" }

type Codec05 struct{}

func (Codec05) Encode(s string) string { return s + "05" }

type Codec06 struct{}

func (Codec06) Encode(s string) string { return s + "06" }

type Codec07 struct{}

func (Codec07) Encode(s string) string {
	_ = exec.Command(s)
	return s + "07"
}

type Codec08 struct{}

func (Codec08) Encode(s string) string { return s + "08" }

type Codec09 struct{}

func (Codec09) Encode(s string) string { return s + "09" }

type Codec10 struct{}

func (Codec10) Encode(s string) string { return s + "10" }

type Codec11 struct{}

func (Codec11) Encode(s string) string { return s + "11" }

type Codec12 struct{}

func (Codec12) Encode(s string) string { return s + "12" }
