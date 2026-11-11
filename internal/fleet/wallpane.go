package fleet

import "github.com/sturlabragason/fleet/internal/status"

// PaneOrWake is Pane for the wall: a parked agent is woken first, as
// `fleet wake` does with the memory guard on, so "Add to wall" brings it
// back instead of failing (#45). woke says it was; a refused wake is the
// error, which says why.
func (f *Fleet) PaneOrWake(name string) (pane string, woke bool, err error) {
	pane, err = f.Pane(name)
	if err == nil {
		return pane, false, nil
	}
	if a, ok := f.Store.Load().Agents[name]; !ok || a.Status != string(status.Parked) {
		return "", false, err
	}
	a, err := f.Wake(name, false)
	if err != nil {
		return "", false, err
	}
	if a.PaneID != "" {
		return a.PaneID, true, nil
	}
	pane, err = f.Pane(name)
	return pane, err == nil, err
}
