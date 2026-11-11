package main

import (
	"reflect"
	"testing"
)

func TestFleetEnvHidesTokens(t *testing.T) {
	got := fleetEnv([]string{"PATH=/bin", "FLEET_WEB_TOKEN=abc", "FLEET_HOME=/h", "FLEET_PEER_KEY=k", "FLEETX=1"})
	if want := []string{"FLEET_HOME=/h", "FLEET_PEER_KEY=(set)", "FLEET_WEB_TOKEN=(set)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%q", got)
	}
}
