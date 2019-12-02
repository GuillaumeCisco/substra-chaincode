// Copyright 2018 Owkin, inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetOutModelHashDress(t *testing.T) {
	scc := new(SubstraChaincode)
	mockStub := NewMockStubWithRegisterNode("substra", scc)
	db := NewLedgerDB(mockStub)

	registerItem(t, *mockStub, "compositeAlgo")
	regular, err := registerTraintuple(mockStub, TraintupleType)
	composite, err := registerTraintuple(mockStub, CompositeTraintupleType)

	// 1. Correct requests

	_, err = db.GetOutModelHashDress(regular, HeadType, []AssetType{TraintupleType})
	assert.NoError(t, err, "the regular traintuple should be found when requesting regular traintuples")

	_, err = db.GetOutModelHashDress(composite, HeadType, []AssetType{CompositeTraintupleType})
	assert.NoError(t, err, "the composite traintuple should be found when requesting composite traintuples")

	// 2. Incorrect requests

	_, err = db.GetOutModelHashDress(regular, HeadType, []AssetType{CompositeTraintupleType})
	assert.Error(t, err, "the regular traintuple should not be found when requesting composite traintuples only")

	_, err = db.GetOutModelHashDress(composite, HeadType, []AssetType{TraintupleType})
	assert.Error(t, err, "the composite traintuple should be found when requesting regular traintuples only")

	// 3. Error messages

	_, err = db.GetOutModelHashDress("abc", HeadType, []AssetType{CompositeTraintupleType})
	assert.Error(t, err, "the key should not be found")
	assert.EqualError(t,
		err,
		"GetOutModelHashDress: Could not find traintuple Head with key \"abc\". Allowed types: [CompositeTraintuple].",
		"the error message should be valid")

	_, err = db.GetOutModelHashDress("abc", HeadType, []AssetType{TraintupleType, CompositeTraintupleType})
	assert.Error(t, err, "the key should not be found")
	assert.EqualError(t,
		err,
		"GetOutModelHashDress: Could not find traintuple Head with key \"abc\". Allowed types: [Traintuple CompositeTraintuple].",
		"the error message should be valid")
}