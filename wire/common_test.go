// Copyright (c) 2013-2016 The btcsuite developers

// Use of this source code is governed by an ISC

// license that can be found in the LICENSE file.

package wire

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSID(t *testing.T) {
	sid, err := ParseSID("LS010001")
	assert.Nil(t, err)
	assert.Equal(t, []byte{byte('L'), byte('S'), byte('0'), byte('1'), byte('0'), byte('0'), byte('0'), byte('1')}, sid[:])
	assert.Equal(t, NodeLogicServer, sid.Type())
	assert.Equal(t, uint16(1), sid.Area())
	assert.Equal(t, uint32(1), sid.NodeID())
	assert.Equal(t, "LS010001", sid.String())
}
