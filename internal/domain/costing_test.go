package domain

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestConsumeFIFO(t *testing.T) {
	layers := []CostLayer{
		{ID: 3, UnitCost: 12_000, QtyRemaining: 10, ReceivedAt: day(3)},
		{ID: 1, UnitCost: 10_000, QtyRemaining: 5, ReceivedAt: day(1)},
		{ID: 2, UnitCost: 11_000, QtyRemaining: 0, ReceivedAt: day(2)},
		{ID: 4, UnitCost: 9_000, QtyRemaining: 4, ReceivedAt: day(3)},
	}
	tests := []struct {
		name      string
		qty       int64
		want      []Consumption
		wantTotal int64
		wantCode  string
	}{
		{
			name:      "within first layer",
			qty:       3,
			want:      []Consumption{{LayerID: 1, Quantity: 3, UnitCost: 10_000}},
			wantTotal: 30_000,
		},
		{
			name: "spans layers, skips empty, ties by id",
			qty:  17,
			want: []Consumption{
				{LayerID: 1, Quantity: 5, UnitCost: 10_000},
				{LayerID: 3, Quantity: 10, UnitCost: 12_000},
				{LayerID: 4, Quantity: 2, UnitCost: 9_000},
			},
			wantTotal: 50_000 + 120_000 + 18_000,
		},
		{
			name: "exactly everything",
			qty:  19,
			want: []Consumption{
				{LayerID: 1, Quantity: 5, UnitCost: 10_000},
				{LayerID: 3, Quantity: 10, UnitCost: 12_000},
				{LayerID: 4, Quantity: 4, UnitCost: 9_000},
			},
			wantTotal: 50_000 + 120_000 + 36_000,
		},
		{name: "too much", qty: 20, wantCode: CodeInsufficientCost},
		{name: "zero", qty: 0, wantCode: CodeValidation},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := ConsumeFIFO(layers, tc.qty)
			if tc.wantCode != "" {
				var de *Error
				if !errors.As(err, &de) || de.Code != tc.wantCode {
					t.Fatalf("err = %v, want code %s", err, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("consumptions = %+v, want %+v", got, tc.want)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
		})
	}
	if layers[0].ID != 3 {
		t.Error("ConsumeFIFO reordered its input")
	}
}

func TestConsumeFIFOOverflow(t *testing.T) {
	_, _, err := ConsumeFIFO([]CostLayer{{ID: 1, UnitCost: math.MaxInt64 / 2, QtyRemaining: 3}}, 3)
	var de *Error
	if !errors.As(err, &de) || de.Code != CodeQuantityOverflow {
		t.Fatalf("expected overflow error, got %v", err)
	}
}
