package quant

import "testing"

func TestScale_Validate(t *testing.T) {
	tests := []struct {
		name    string
		scale   Scale
		wantErr bool
	}{
		{"min", MinScale, false},
		{"max", MaxScale, false},
		{"mid", 8, false},
		{"below min", -1, true},
		{"above max", MaxScale + 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scale.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Scale(%d).Validate() error = %v, wantErr %v", tt.scale, err, tt.wantErr)
			}
		})
	}
}
