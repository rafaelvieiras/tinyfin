package storage

import "strings"

// VideoRangeOf is the VideoRange and VideoRangeType of a video stream
// (playback.offer.R13). Without Dolby Vision the color transfer decides (PQ is
// HDR10, or HDR10Plus when the first frame carries dynamic metadata; HLG is
// HLG; anything else SDR). A Dolby Vision record decides the type by profile
// and base layer signal compatibility: 5 is DOVI, 7 DOVIWithEL (or
// DOVIWithELHDR10Plus), 8 (and 10) DOVIWithHDR10 / DOVIWithHDR10Plus /
// DOVIWithHLG / DOVIWithSDR when the transfer agrees with the base layer and
// DOVIInvalid when it does not, any other profile or an unknown compatibility
// id counts as no record. A Dolby Vision codec tag (dvh1, dvhe) without a
// record is SDR.
func VideoRangeOf(s MediaStreamRef) (string, string) {
	rng, typ := "SDR", "SDR"
	switch strings.ToLower(s.ColorTransfer) {
	case "smpte2084":
		rng, typ = "HDR", "HDR10"
		if s.Hdr10Plus {
			typ = "HDR10Plus"
		}
	case "arib-std-b67":
		rng, typ = "HDR", "HLG"
	}
	dv := s.DoVi
	if dv == nil {
		if tag := strings.ToLower(s.CodecTag); tag == "dvh1" || tag == "dvhe" {
			return "SDR", "SDR"
		}
		return rng, typ
	}
	pq, hlg := strings.EqualFold(s.ColorTransfer, "smpte2084"), strings.EqualFold(s.ColorTransfer, "arib-std-b67")
	switch dv.Profile {
	case 5:
		return "HDR", "DOVI"
	case 7:
		if s.Hdr10Plus {
			return "HDR", "DOVIWithELHDR10Plus"
		}
		return "HDR", "DOVIWithEL"
	case 8, 10:
		switch dv.CompatibilityID {
		case 0:
			if dv.Profile == 10 {
				return "HDR", "DOVI"
			}
			return "HDR", "DOVIInvalid"
		case 1:
			switch {
			case pq && s.Hdr10Plus:
				return "HDR", "DOVIWithHDR10Plus"
			case pq:
				return "HDR", "DOVIWithHDR10"
			}
			return "HDR", "DOVIInvalid"
		case 2:
			return "SDR", "DOVIWithSDR"
		case 4:
			if hlg {
				return "HDR", "DOVIWithHLG"
			}
			return "HDR", "DOVIInvalid"
		case 6:
			return "HDR", "DOVIInvalid"
		}
	}
	return rng, typ
}
