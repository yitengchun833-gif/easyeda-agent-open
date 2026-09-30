package app

import "testing"

func TestVariantPreservesMeasuredPinDirectionThroughRotation(t *testing.T) {
	in := variantSafetyFixture(t)
	zone := in.Zones[0]
	for i := range zone.Layout.Placements {
		part := &zone.Layout.Placements[i]
		for j := range part.Pins {
			direction, err := libPinSide(part.Pins[j], part.BBox)
			if err != nil {
				t.Fatal(err)
			}
			angle := map[string]float64{"right": 0, "up": 90, "left": 180, "down": 270}[direction]
			part.Pins[j].Rotation = &angle
		}
	}
	alt := cloneVariantInput(t, SchematicRenderInput{SchemaVersion: 1, Zones: []SchematicRenderZone{zone}}).Zones[0].Layout
	alt.Placements[1] = plTranslate(plRotate(alt.Placements[1], 1), 50, 100)
	if err := validateSchematicVariantPreservation(zone, alt); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"drop", "wrong-angle"} {
		t.Run(mode, func(t *testing.T) {
			copy := cloneVariantInput(t, SchematicRenderInput{SchemaVersion: 1, Zones: []SchematicRenderZone{{Layout: alt}}}).Zones[0].Layout
			if mode == "drop" {
				copy.Placements[1].Pins[0].Rotation = nil
			} else {
				angle := *copy.Placements[1].Pins[0].Rotation + 90
				copy.Placements[1].Pins[0].Rotation = &angle
			}
			if err := validateSchematicVariantPreservation(zone, copy); err == nil {
				t.Fatal("accepted lost or forged measured pin direction")
			}
		})
	}
}

func TestVariantCannotInventOfficialPinDirection(t *testing.T) {
	in := variantSafetyFixture(t)
	alt := cloneVariantInput(t, in).Zones[0].Variants[1].Layout
	angle := 180.0
	alt.Placements[1].Pins[0].Rotation = &angle
	if err := validateSchematicVariantPreservation(in.Zones[0], alt); err == nil {
		t.Fatal("accepted fabricated official direction on legacy source")
	}
}
