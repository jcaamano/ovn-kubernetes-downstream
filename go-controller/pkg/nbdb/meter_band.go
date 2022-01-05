// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package nbdb

import "github.com/ovn-org/libovsdb/model"

type (
	MeterBandAction = string
)

var (
	MeterBandActionDrop MeterBandAction = "drop"
)

// MeterBand defines an object in Meter_Band table
type MeterBand struct {
	UUID        string            `ovsdb:"_uuid"`
	Action      MeterBandAction   `ovsdb:"action"`
	BurstSize   int               `ovsdb:"burst_size"`
	ExternalIDs map[string]string `ovsdb:"external_ids"`
	Rate        int               `ovsdb:"rate"`
}

func copyMeterBandExternalIDs(a map[string]string) map[string]string {
	if a == nil {
		return nil
	}
	b := make(map[string]string, len(a))
	for k, v := range a {
		b[k] = v
	}
	return b
}

func equalMeterBandExternalIDs(a, b map[string]string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}

func (a *MeterBand) DeepCopyInto(b *MeterBand) {
	*b = *a
	b.ExternalIDs = copyMeterBandExternalIDs(a.ExternalIDs)
}

func (a *MeterBand) DeepCopy() *MeterBand {
	b := new(MeterBand)
	a.DeepCopyInto(b)
	return b
}

func (a *MeterBand) CloneModelInto(b model.Model) {
	c := b.(*MeterBand)
	a.DeepCopyInto(c)
}

func (a *MeterBand) CloneModel() model.Model {
	return a.DeepCopy()
}

func (a *MeterBand) Equals(b *MeterBand) bool {
	return a.UUID == b.UUID &&
		a.Action == b.Action &&
		a.BurstSize == b.BurstSize &&
		equalMeterBandExternalIDs(a.ExternalIDs, b.ExternalIDs) &&
		a.Rate == b.Rate
}

func (a *MeterBand) EqualsModel(b model.Model) bool {
	c := b.(*MeterBand)
	return a.Equals(c)
}

var _ model.CloneableModel = &MeterBand{}
var _ model.ComparableModel = &MeterBand{}
