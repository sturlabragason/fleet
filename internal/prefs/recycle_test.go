package prefs

import "testing"

func TestRecycle(t *testing.T) {
	write(t, "[defaults]\nmax_working = 2\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Recycle(); got != DefaultRecycle() {
		t.Fatalf("unset: %+v", got)
	}
	write(t, "[defaults]\nrecycle_compactions = 0\nrecycle_aic = 250.5\n")
	if p, err = Load(); err != nil {
		t.Fatal(err)
	}
	r := p.Recycle()
	if r.Compactions != 0 || r.AIC != 250.5 || r.Off() {
		t.Fatalf("set: %+v", r)
	}
	if r.Due(9, 250) || !r.Due(0, 250.5) {
		t.Fatal("compactions off, credits on")
	}
	if (Recycle{}).Due(5, 1e6) {
		t.Fatal("both off must never be due")
	}
	write(t, "[defaults]\nrecycle_aic = lots\n")
	if _, err := Load(); err == nil {
		t.Fatal("bad recycle_aic accepted")
	}
}
