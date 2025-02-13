package interop

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/prysmaticlabs/go-ssz"
	ethpb "github.com/prysmaticlabs/prysm/proto/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/featureconfig"
)

// WriteBlockToDisk as a block ssz. Writes to temp directory. Debug!
func WriteBlockToDisk(block *ethpb.BeaconBlock, failed bool) {
	if !featureconfig.FeatureConfig().WriteSSZStateTransitions {
		return
	}

	filename := fmt.Sprintf("beacon_block_%d.ssz", block.Slot)
	if failed {
		filename = "failed_" + filename
	}
	fp := path.Join(os.TempDir(), filename)
	log.Warnf("Writing block to disk at %s", fp)
	enc, err := ssz.Marshal(block)
	if err != nil {
		log.WithError(err).Error("Failed to ssz encode block")
		return
	}
	if err := ioutil.WriteFile(fp, enc, 0664); err != nil {
		log.WithError(err).Error("Failed to write to disk")
	}
}
