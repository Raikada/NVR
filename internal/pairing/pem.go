package pairing

import "encoding/pem"

// decodePEMBlock is a thin wrapper that returns the first PEM block
// and the remaining bytes. Used to walk multi-cert PEM bundles.
func decodePEMBlock(b []byte) (*pem.Block, []byte) {
	return pem.Decode(b)
}
