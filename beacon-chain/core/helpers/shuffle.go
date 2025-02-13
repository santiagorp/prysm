package helpers

import (
	"encoding/binary"
	"fmt"

	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/hashutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/sliceutil"
)

const seedSize = int8(32)
const roundSize = int8(1)
const positionWindowSize = int8(4)
const pivotViewSize = seedSize + roundSize
const totalSize = seedSize + roundSize + positionWindowSize

var maxShuffleListSize uint64 = 1 << 40

// SplitIndices splits a list into n pieces.
func SplitIndices(l []uint64, n uint64) [][]uint64 {
	var divided [][]uint64
	var lSize = uint64(len(l))
	for i := uint64(0); i < n; i++ {
		start := sliceutil.SplitOffset(lSize, n, i)
		end := sliceutil.SplitOffset(lSize, n, i+1)
		divided = append(divided, l[start:end])
	}
	return divided
}

// ShuffledIndex returns `p(index)` in a pseudorandom permutation `p` of `0...list_size - 1` with ``seed`` as entropy.
// We utilize 'swap or not' shuffling in this implementation; we are allocating the memory with the seed that stays
// constant between iterations instead of reallocating it each iteration as in the spec. This implementation is based
// on the original implementation from protolambda, https://github.com/protolambda/eth2-shuffle
func ShuffledIndex(index uint64, indexCount uint64, seed [32]byte) (uint64, error) {
	return innerShuffledIndex(index, indexCount, seed, true /* shuffle */)
}

// UnShuffledIndex returns the inverse of ShuffledIndex. This implementation is based
// on the original implementation from protolambda, https://github.com/protolambda/eth2-shuffle
func UnShuffledIndex(index uint64, indexCount uint64, seed [32]byte) (uint64, error) {
	return innerShuffledIndex(index, indexCount, seed, false /* un-shuffle */)
}

// Spec pseudocode definition:
//   def compute_shuffled_index(index: ValidatorIndex, index_count: uint64, seed: Hash) -> ValidatorIndex:
//    """
//    Return the shuffled validator index corresponding to ``seed`` (and ``index_count``).
//    """
//    assert index < index_count
//
//    # Swap or not (https://link.springer.com/content/pdf/10.1007%2F978-3-642-32009-5_1.pdf)
//    # See the 'generalized domain' algorithm on page 3
//    for current_round in range(SHUFFLE_ROUND_COUNT):
//        pivot = bytes_to_int(hash(seed + int_to_bytes(current_round, length=1))[0:8]) % index_count
//        flip = ValidatorIndex((pivot + index_count - index) % index_count)
//        position = max(index, flip)
//        source = hash(seed + int_to_bytes(current_round, length=1) + int_to_bytes(position // 256, length=4))
//        byte = source[(position % 256) // 8]
//        bit = (byte >> (position % 8)) % 2
//        index = flip if bit else index
//
//    return ValidatorIndex(index)
func innerShuffledIndex(index uint64, indexCount uint64, seed [32]byte, shuffle bool) (uint64, error) {
	if params.BeaconConfig().ShuffleRoundCount == 0 {
		return index, nil
	}
	if index >= indexCount {
		return 0, fmt.Errorf("input index %d out of bounds: %d",
			index, indexCount)
	}
	if indexCount > maxShuffleListSize {
		return 0, fmt.Errorf("list size %d out of bounds",
			indexCount)
	}
	rounds := uint8(params.BeaconConfig().ShuffleRoundCount)
	round := uint8(0)
	if !shuffle {
		// Starting last round and iterating through the rounds in reverse, un-swaps everything,
		// effectively un-shuffling the list.
		round = rounds - 1
	}
	buf := make([]byte, totalSize, totalSize)
	// Seed is always the first 32 bytes of the hash input, we never have to change this part of the buffer.
	copy(buf[:32], seed[:])
	for {
		buf[seedSize] = round
		hash := hashutil.Hash(buf[:pivotViewSize])
		hash8 := hash[:8]
		hash8Int := bytesutil.FromBytes8(hash8)
		pivot := hash8Int % indexCount
		flip := (pivot + indexCount - index) % indexCount
		// Consider every pair only once by picking the highest pair index to retrieve randomness.
		position := index
		if flip > position {
			position = flip
		}
		// Add position except its last byte to []buf for randomness,
		// it will be used later to select a bit from the resulting hash.
		position4bytes := bytesutil.ToBytes(position>>8, 4)
		copy(buf[pivotViewSize:], position4bytes[:])
		source := hashutil.Hash(buf)
		// Effectively keep the first 5 bits of the byte value of the position,
		// and use it to retrieve one of the 32 (= 2^5) bytes of the hash.
		byteV := source[(position&0xff)>>3]
		// Using the last 3 bits of the position-byte, determine which bit to get from the hash-byte (note: 8 bits = 2^3)
		bitV := (byteV >> (position & 0x7)) & 0x1
		// index = flip if bit else index
		if bitV == 1 {
			index = flip
		}
		if shuffle {
			round++
			if round == rounds {
				break
			}
		} else {
			if round == 0 {
				break
			}
			round--
		}
	}
	return index, nil
}

// ShuffleList returns list of shuffled indexes in a pseudorandom permutation `p` of `0...list_size - 1` with ``seed`` as entropy.
// We utilize 'swap or not' shuffling in this implementation; we are allocating the memory with the seed that stays
// constant between iterations instead of reallocating it each iteration as in the spec. This implementation is based
// on the original implementation from protolambda, https://github.com/protolambda/eth2-shuffle
//  improvements:
//   - seed is always the first 32 bytes of the hash input, we just copy it into the buffer one time.
//   - add round byte to seed and hash that part of the buffer.
//   - split up the for-loop in two:
//    1. Handle the part from 0 (incl) to pivot (incl). This is mirrored around (pivot / 2).
//    2. Handle the part from pivot (excl) to N (excl). This is mirrored around ((pivot / 2) + (size/2)).
//   - hash source every 256 iterations.
//   - change byteV every 8 iterations.
//   - we start at the edges, and work back to the mirror point.
//     this makes us process each pear exactly once (instead of unnecessarily twice, like in the spec).
func ShuffleList(input []uint64, seed [32]byte) ([]uint64, error) {
	return innerShuffleList(input, seed, true /* shuffle */)
}

// UnshuffleList un-shuffles the list by running backwards through the round count.
func UnshuffleList(input []uint64, seed [32]byte) ([]uint64, error) {
	return innerShuffleList(input, seed, false /* un-shuffle */)
}

// shuffles or unshuffles, shuffle=false to un-shuffle.
func innerShuffleList(input []uint64, seed [32]byte, shuffle bool) ([]uint64, error) {
	if len(input) <= 1 {
		return input, nil
	}
	if uint64(len(input)) > maxShuffleListSize {
		return nil, fmt.Errorf("list size %d out of bounds",
			len(input))
	}
	rounds := uint8(params.BeaconConfig().ShuffleRoundCount)
	if rounds == 0 {
		return input, nil
	}
	listSize := uint64(len(input))
	buf := make([]byte, totalSize, totalSize)
	r := uint8(0)
	if !shuffle {
		r = rounds - 1
	}
	copy(buf[:seedSize], seed[:])
	for {
		buf[seedSize] = r
		ph := hashutil.Hash(buf[:pivotViewSize])
		pivot := bytesutil.FromBytes8(ph[:8]) % listSize
		mirror := (pivot + 1) >> 1
		binary.LittleEndian.PutUint32(buf[pivotViewSize:], uint32(pivot>>8))
		source := hashutil.Hash(buf)
		byteV := source[(pivot&0xff)>>3]
		for i, j := uint64(0), pivot; i < mirror; i, j = i+1, j-1 {
			byteV, source = swapOrNot(buf, byteV, i, input, j, source)
		}
		// Now repeat, but for the part after the pivot.
		mirror = (pivot + listSize + 1) >> 1
		end := listSize - 1
		binary.LittleEndian.PutUint32(buf[pivotViewSize:], uint32(end>>8))
		source = hashutil.Hash(buf)
		byteV = source[(end&0xff)>>3]
		for i, j := pivot+1, end; i < mirror; i, j = i+1, j-1 {
			byteV, source = swapOrNot(buf, byteV, i, input, j, source)
		}
		if shuffle {
			r++
			if r == rounds {
				break
			}
		} else {
			if r == 0 {
				break
			}
			r--
		}
	}
	return input, nil
}

// swapOrNot describes the main algorithm behind the shuffle where we swap bytes in the inputted value
// depending on if the conditions are met.
func swapOrNot(buf []byte, byteV byte, i uint64, input []uint64, j uint64, source [32]byte) (byte, [32]byte) {
	if j&0xff == 0xff {
		// just overwrite the last part of the buffer, reuse the start (seed, round)
		binary.LittleEndian.PutUint32(buf[pivotViewSize:], uint32(j>>8))
		source = hashutil.Hash(buf)
	}
	if j&0x7 == 0x7 {
		byteV = source[(j&0xff)>>3]
	}
	bitV := (byteV >> (j & 0x7)) & 0x1

	if bitV == 1 {
		input[i], input[j] = input[j], input[i]
	}
	return byteV, source
}
