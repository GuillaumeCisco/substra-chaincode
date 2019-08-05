package main

import (
	"chaincode/errors"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"encoding/json"

	"github.com/hyperledger/fabric/core/chaincode/shim"
)

// List of the possible tuple's status
const (
	StatusDoing   = "doing"
	StatusTodo    = "todo"
	StatusWaiting = "waiting"
	StatusFailed  = "failed"
	StatusDone    = "done"
)

// -------------------------------------------------------------------------------------------
// Methods on receivers traintuple
// -------------------------------------------------------------------------------------------

// SetFromInput is a method of the receiver Traintuple.
// It uses the inputTraintuple to check and set the traintuple's parameters
// which don't depend on previous traintuples values :
//  - AssetType
//  - Creator & permissions
//  - Tag
//  - AlgoKey & ObjectiveKey
//  - Dataset
func (traintuple *Traintuple) SetFromInput(stub shim.ChaincodeStubInterface, inp inputTraintuple) error {

	// TODO later: check permissions
	// find associated creator and check permissions (TODO later)
	creator, err := getTxCreator(stub)
	if err != nil {
		return err
	}
	traintuple.AssetType = TraintupleType
	traintuple.Creator = creator
	traintuple.Permissions = "all"
	traintuple.Tag = inp.Tag
	// check if algo exists
	if _, err = getElementBytes(stub, inp.AlgoKey); err != nil {
		return errors.BadRequest(err, "could not retrieve algo with key %s", inp.AlgoKey)
	}
	traintuple.AlgoKey = inp.AlgoKey

	// check objective exists
	if _, err = getElementBytes(stub, inp.ObjectiveKey); err != nil {
		return errors.BadRequest(err, "could not retrieve objective with key %s", inp.ObjectiveKey)
	}
	traintuple.ObjectiveKey = inp.ObjectiveKey

	// check if DataSampleKeys are from the same dataManager and if they are not test only dataSample
	_, trainOnly, err := checkSameDataManager(stub, inp.DataManagerKey, inp.DataSampleKeys)
	if err != nil {
		return err
	}
	if !trainOnly {
		return errors.BadRequest("not possible to create a traintuple with test only data")
	}
	if _, err = getElementBytes(stub, inp.DataManagerKey); err != nil {
		return errors.BadRequest(err, "could not retrieve dataManager with key %s", inp.DataManagerKey)
	}

	// fill traintuple.Dataset from dataManager and dataSample
	traintuple.Dataset = &Dataset{
		DataManagerKey: inp.DataManagerKey,
		DataSampleKeys: inp.DataSampleKeys,
	}
	traintuple.Dataset.Worker, err = getDataManagerOwner(stub, traintuple.Dataset.DataManagerKey)
	return err
}

// SetFromParents set the status of the traintuple depending on its "parents",
// i.e. the traintuples from which it received the outModels as inModels.
// Also it's InModelKeys are set.
func (traintuple *Traintuple) SetFromParents(stub shim.ChaincodeStubInterface, inModels []string) error {
	var err error
	status := StatusTodo
	parentTraintupleKeys := inModels
	for _, parentTraintupleKey := range parentTraintupleKeys {
		parentTraintuple := Traintuple{}
		if err = getElementStruct(stub, parentTraintupleKey, &parentTraintuple); err != nil {
			err = errors.BadRequest(err, "could not retrieve parent traintuple with key %s %d", parentTraintupleKeys, len(parentTraintupleKeys))
			return err
		}
		// set traintuple to waiting if one of the parent traintuples is not done
		if parentTraintuple.OutModel == nil {
			status = StatusWaiting
		}
		traintuple.InModelKeys = append(traintuple.InModelKeys, parentTraintupleKey)
	}
	traintuple.Status = status
	return nil
}

// GetKey return the key of the traintuple depending on its key parameters.
func (traintuple *Traintuple) GetKey(stub shim.ChaincodeStubInterface) (string, error) {
	hashKeys := []string{traintuple.Creator, traintuple.AlgoKey, traintuple.Dataset.DataManagerKey}
	hashKeys = append(hashKeys, traintuple.Dataset.DataSampleKeys...)
	hashKeys = append(hashKeys, traintuple.InModelKeys...)
	return HashForKey(stub, "traintuple", hashKeys...)

}

// AddToFLTask set the traintuple's parameters that determines if it's part of on FLTask and how.
// It uses the inputTraintuple values as follow:
//  - If neither FLTask nor rank is set it returns immediately
//  - If rank is 0 and FLTask empty, it's start a new one using this traintuple key
//  - If rank and FLTask are set, it checks if there are coherent with previous ones and set it.
func (traintuple *Traintuple) AddToFLTask(stub shim.ChaincodeStubInterface, inp inputTraintuple, traintupleKey string) error {
	// check FLTask and Rank and set it when required
	var err error
	if inp.Rank == "" {
		if inp.FLTask != "" {
			err = errors.BadRequest("invalid inputs, a FLTask should have a rank")
			return err
		}
		return nil
	}
	traintuple.Rank, err = strconv.Atoi(inp.Rank)
	if err != nil {
		return err
	}
	if inp.FLTask == "" {
		if traintuple.Rank != 0 {
			err = errors.BadRequest("invalid inputs, a new FLTask should have a rank 0")
			return err
		}
		traintuple.FLTask = traintupleKey
		return nil
	}
	var ttKeys []string
	attributes := []string{"traintuple", inp.FLTask}
	ttKeys, err = getKeysFromComposite(stub, "traintuple~fltask~worker~rank~key", attributes)
	if err != nil {
		return err
	} else if len(ttKeys) == 0 {
		err = errors.BadRequest("cannot find the FLTask %s", inp.FLTask)
		return err
	}
	for _, ttKey := range ttKeys {
		FLTraintuple := Traintuple{}
		err = getElementStruct(stub, ttKey, &FLTraintuple)
		if err != nil {
			return err
		} else if FLTraintuple.AlgoKey != inp.AlgoKey {
			err = errors.BadRequest("previous traintuple for FLTask %s does not have the same algo key %s", inp.FLTask, inp.AlgoKey)
			return err
		}
	}

	attributes = []string{"traintuple", inp.FLTask, traintuple.Dataset.Worker, inp.Rank}
	ttKeys, err = getKeysFromComposite(stub, "traintuple~fltask~worker~rank~key", attributes)
	if err != nil {
		return err
	} else if len(ttKeys) > 0 {
		err = errors.BadRequest("FLTask %s with worker %s rank %d already exists", inp.FLTask, traintuple.Dataset.Worker, traintuple.Rank)
		return err
	}

	traintuple.FLTask = inp.FLTask

	return nil
}

// Save will put in the legder interface both the traintuple with its key
// and all the associated composite keys
func (traintuple *Traintuple) Save(stub shim.ChaincodeStubInterface, traintupleKey string) error {

	// store in ledger
	traintupleBytes, _ := json.Marshal(traintuple)
	if err := stub.PutState(traintupleKey, traintupleBytes); err != nil {
		err = fmt.Errorf("could not put in ledger traintuple with algo %s inModels %s - %s", traintuple.AlgoKey, traintuple.InModelKeys, err.Error())
		return err
	}

	// create composite keys
	if err := createCompositeKey(stub, "traintuple~algo~key", []string{"traintuple", traintuple.AlgoKey, traintupleKey}); err != nil {
		err = fmt.Errorf("issue creating composite keys - %s", err.Error())
		return err
	}
	if err := createCompositeKey(stub, "traintuple~worker~status~key", []string{"traintuple", traintuple.Dataset.Worker, traintuple.Status, traintupleKey}); err != nil {
		err = fmt.Errorf("issue creating composite keys - %s", err.Error())
		return err
	}
	for _, inModelKey := range traintuple.InModelKeys {
		if err := createCompositeKey(stub, "traintuple~inModel~key", []string{"traintuple", inModelKey, traintupleKey}); err != nil {
			err = fmt.Errorf("issue creating composite keys - %s", err.Error())
			return err
		}
	}
	if traintuple.FLTask != "" {
		if err := createCompositeKey(stub, "traintuple~fltask~worker~rank~key", []string{"traintuple", traintuple.FLTask, traintuple.Dataset.Worker, strconv.Itoa(traintuple.Rank), traintupleKey}); err != nil {
			err = fmt.Errorf("issue creating composite keys - %s", err.Error())
			return err
		}
	}
	if traintuple.Tag != "" {
		err := createCompositeKey(stub, "traintuple~tag~key", []string{"traintuple", traintuple.Tag, traintupleKey})
		if err != nil {
			return err
		}
	}
	return nil
}

// -------------------------------------------------------------------------------------------
// Methods on receivers testtuple
// -------------------------------------------------------------------------------------------

// SetFromInput is a method of the receiver Testtuple.
// It uses the inputTesttuple to check and set the testtuple's parameters
// which don't depend on previous testtuples values :
//  - AssetType
//  - Creator & permissions
//  - Tag
//  - AlgoKey & ObjectiveKey
//  - Dataset
//  - Certified
func (testtuple *Testtuple) SetFromInput(stub shim.ChaincodeStubInterface, inp inputTesttuple) error {

	// TODO later: check permissions
	// find associated creator and check permissions (TODO later)
	creator, err := getTxCreator(stub)
	if err != nil {
		return err
	}
	testtuple.Creator = creator
	testtuple.Permissions = "all"
	testtuple.Tag = inp.Tag
	testtuple.AssetType = TesttupleType

	// Get test dataset from objective
	objective := Objective{}
	if err = getElementStruct(stub, testtuple.ObjectiveKey, &objective); err != nil {
		return errors.BadRequest(err, "could not retrieve objective with key %s", testtuple.ObjectiveKey)
	}
	var objectiveDataManagerKey string
	var objectiveDataSampleKeys []string
	if objective.TestDataset != nil {
		objectiveDataManagerKey = objective.TestDataset.DataManagerKey
		objectiveDataSampleKeys = objective.TestDataset.DataSampleKeys
	}
	// For now we need to sort it but in fine it should be save sorted
	// TODO
	sort.Strings(objectiveDataSampleKeys)

	var dataManagerKey string
	var dataSampleKeys []string
	if len(inp.DataManagerKey) > 0 && len(inp.DataSampleKeys) > 0 {
		// non-certified testtuple
		// test dataset are specified by the user
		dataSampleKeys = inp.DataSampleKeys
		_, _, err = checkSameDataManager(stub, inp.DataManagerKey, dataSampleKeys)
		if err != nil {
			return err
		}
		dataManagerKey = inp.DataManagerKey
		sort.Strings(dataSampleKeys)
		testtuple.Certified = objectiveDataManagerKey == dataManagerKey && reflect.DeepEqual(objectiveDataSampleKeys, dataSampleKeys)
	} else if len(inp.DataManagerKey) > 0 || len(inp.DataSampleKeys) > 0 {
		return errors.BadRequest("invalid input: dataManagerKey and dataSampleKey should be provided together")
	} else if objective.TestDataset != nil {
		dataSampleKeys = objectiveDataSampleKeys
		dataManagerKey = objectiveDataManagerKey
		testtuple.Certified = true
	} else {
		return errors.BadRequest("can not create a certified testtuple, no data associated with objective %s", testtuple.ObjectiveKey)
	}
	// retrieve dataManager owner
	dataManager := DataManager{}
	if err = getElementStruct(stub, dataManagerKey, &dataManager); err != nil {
		return errors.BadRequest(err, "could not retrieve dataManager with key %s", dataManagerKey)
	}
	testtuple.Dataset = &TtDataset{
		Worker:         dataManager.Owner,
		DataSampleKeys: dataSampleKeys,
		OpenerHash:     dataManagerKey,
	}
	return nil
}

// SetFromTraintuple set the parameters of the testuple depending on traintuple
// it depends on. It sets:
//  - AlgoKey
//  - ObjectiveKey
//  - Model
//  - Status
func (testtuple *Testtuple) SetFromTraintuple(stub shim.ChaincodeStubInterface, traintupleKey string) error {

	// check associated traintuple
	traintuple := Traintuple{}
	if err := getElementStruct(stub, traintupleKey, &traintuple); err != nil {
		return errors.BadRequest(err, "could not retrieve traintuple with key %s", traintupleKey)
	}
	testtuple.ObjectiveKey = traintuple.ObjectiveKey
	testtuple.AlgoKey = traintuple.AlgoKey
	testtuple.Model = &Model{
		TraintupleKey: traintupleKey,
	}
	if traintuple.OutModel != nil {
		testtuple.Model.Hash = traintuple.OutModel.Hash
		testtuple.Model.StorageAddress = traintuple.OutModel.StorageAddress
	}

	switch status := traintuple.Status; status {
	case StatusDone:
		testtuple.Status = StatusTodo
	case StatusFailed:
		return errors.BadRequest(
			"could not register this testtuple, the traintuple %s has a failed status",
			traintupleKey)
	default:
		testtuple.Status = StatusWaiting
	}
	return nil
}

// GetKey return the key of the testuple depending on its key parameters.
func (testtuple *Testtuple) GetKey(stub shim.ChaincodeStubInterface) (string, error) {
	// create testtuple key and check if it already exists
	hashKeys := []string{
		testtuple.Model.TraintupleKey,
		testtuple.Dataset.OpenerHash,
		testtuple.Creator,
	}
	hashKeys = append(hashKeys, testtuple.Dataset.DataSampleKeys...)
	return HashForKey(stub, "testtuple", hashKeys...)
}

// Save will put in the legder interface both the testtuple with its key
// and all the associated composite keys
func (testtuple *Testtuple) Save(stub shim.ChaincodeStubInterface, testtupleKey string) error {
	var err error
	testtupleBytes, _ := json.Marshal(testtuple)
	if err = stub.PutState(testtupleKey, testtupleBytes); err != nil {
		err = fmt.Errorf("could not put in ledger testtuple associated with traintuple %s - %s", testtuple.Model.TraintupleKey, err.Error())
		return err
	}

	// create composite keys
	if err = createCompositeKey(stub, "testtuple~algo~key", []string{"testtuple", testtuple.AlgoKey, testtupleKey}); err != nil {
		err = fmt.Errorf("issue creating composite keys - %s", err.Error())
		return err
	}
	if err = createCompositeKey(stub, "testtuple~worker~status~key", []string{"testtuple", testtuple.Dataset.Worker, testtuple.Status, testtupleKey}); err != nil {
		err = fmt.Errorf("issue creating composite keys - %s", err.Error())
		return err
	}
	if err = createCompositeKey(stub, "testtuple~traintuple~certified~key", []string{"testtuple", testtuple.Model.TraintupleKey, strconv.FormatBool(testtuple.Certified), testtupleKey}); err != nil {
		err = fmt.Errorf("issue creating composite keys - %s", err.Error())
		return err
	}
	if testtuple.Tag != "" {
		err = createCompositeKey(stub, "testtuple~tag~key", []string{"traintuple", testtuple.Tag, testtupleKey})
		if err != nil {
			return err
		}
	}
	return nil
}

// -------------------------------------------------------------------------------------------
// Smart contracts related to traintuples and testuples
// args  [][]byte or []string, it is not possible to input a string looking like a json
// -------------------------------------------------------------------------------------------

// createComputePlan is the wrapper for the substra smartcontract CreateComputePlan
func createComputePlan(stub shim.ChaincodeStubInterface, args []string) (resp outputComputePlan, err error) {
	inp := inputComputePlan{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}

	traintupleKeysByID := map[string]string{}
	resp.TraintupleKeys = []string{}
	var traintuplesTodo []outputTraintuple
	for i, computeTraintuple := range inp.Traintuples {
		inpTraintuple := inputTraintuple{}
		inpTraintuple.AlgoKey = inp.AlgoKey
		inpTraintuple.ObjectiveKey = inp.ObjectiveKey
		inpTraintuple.DataManagerKey = computeTraintuple.DataManagerKey
		inpTraintuple.DataSampleKeys = computeTraintuple.DataSampleKeys
		inpTraintuple.Tag = computeTraintuple.Tag
		inpTraintuple.Rank = strconv.Itoa(i)

		traintuple := Traintuple{}
		err := traintuple.SetFromInput(stub, inpTraintuple)
		if err != nil {
			return resp, err
		}

		// Set the inModels by matching the id to traintuples key previously
		// encontered in this compute plan
		for _, InModelID := range computeTraintuple.InModelsIDs {
			inModelKey, ok := traintupleKeysByID[InModelID]
			if !ok {
				return resp, errors.BadRequest("traintuple ID %s: model ID %s not found, check traintuple list order", computeTraintuple.ID, InModelID)
			}
			traintuple.InModelKeys = append(traintuple.InModelKeys, inModelKey)
		}

		traintupleKey, err := traintuple.GetKey(stub)
		if err != nil {
			return resp, errors.Conflict(err)
		}

		// Set the Fltask
		if i == 0 {
			traintuple.FLTask = traintupleKey
			resp.FLTask = traintuple.FLTask
		} else {
			traintuple.FLTask = resp.FLTask
		}

		// Set status: if it has parents it's waiting
		// if not it's todo and it has to be included in the event
		if len(computeTraintuple.InModelsIDs) > 0 {
			traintuple.Status = StatusWaiting
		} else {
			traintuple.Status = StatusTodo
			out := outputTraintuple{}
			err = out.Fill(stub, traintuple, traintupleKey)
			if err != nil {
				return resp, err
			}
			traintuplesTodo = append(traintuplesTodo, out)
		}

		err = traintuple.Save(stub, traintupleKey)
		if err != nil {
			return resp, errors.E(err, "could not create traintuple with ID %s", computeTraintuple.ID)
		}
		traintupleKeysByID[computeTraintuple.ID] = traintupleKey
		resp.TraintupleKeys = append(resp.TraintupleKeys, traintupleKey)
	}

	resp.TesttupleKeys = []string{}
	for index, computeTesttuple := range inp.Testtuples {
		traintupleKey, ok := traintupleKeysByID[computeTesttuple.TraintupleID]
		if !ok {
			return resp, errors.BadRequest("testtuple index %s: traintuple ID %s not found", index, computeTesttuple.TraintupleID)
		}
		testtuple := Testtuple{}
		testtuple.Model = &Model{TraintupleKey: traintupleKey}
		testtuple.ObjectiveKey = inp.ObjectiveKey
		testtuple.AlgoKey = inp.AlgoKey

		inputTesttuple := inputTesttuple{}
		inputTesttuple.DataManagerKey = computeTesttuple.DataManagerKey
		inputTesttuple.DataSampleKeys = computeTesttuple.DataSampleKeys
		inputTesttuple.Tag = computeTesttuple.Tag
		err = testtuple.SetFromInput(stub, inputTesttuple)
		if err != nil {
			return resp, err
		}
		testtuple.Status = StatusWaiting
		testtupleKey, err := testtuple.GetKey(stub)
		if err != nil {
			return resp, errors.Conflict(err)
		}
		err = testtuple.Save(stub, testtupleKey)
		if err != nil {
			return resp, err
		}
		resp.TesttupleKeys = append(resp.TesttupleKeys, testtupleKey)
	}

	event := TuplesEvent{}
	event.SetTraintuples(traintuplesTodo...)

	err = SetEvent(stub, "tuples-updated", event)
	if err != nil {
		return resp, err
	}

	return resp, err
}

// createTraintuple adds a Traintuple in the ledger
func createTraintuple(stub shim.ChaincodeStubInterface, args []string) (map[string]string, error) {
	inp := inputTraintuple{}
	err := AssetFromJSON(args, &inp)
	if err != nil {
		return nil, err
	}

	traintuple := Traintuple{}
	err = traintuple.SetFromInput(stub, inp)
	if err != nil {
		return nil, err
	}
	err = traintuple.SetFromParents(stub, inp.InModels)
	if err != nil {
		return nil, err
	}
	traintupleKey, err := traintuple.GetKey(stub)
	if err != nil {
		return nil, errors.Conflict(err)
	}
	err = traintuple.AddToFLTask(stub, inp, traintupleKey)
	if err != nil {
		return nil, err
	}
	err = traintuple.Save(stub, traintupleKey)
	if err != nil {
		return nil, err
	}
	out := outputTraintuple{}
	err = out.Fill(stub, traintuple, traintupleKey)
	if err != nil {
		return nil, err
	}

	// https://github.com/hyperledger/fabric/blob/release-1.4/core/chaincode/shim/interfaces.go#L339:L343
	// We can only send one event per transaction
	// https://stackoverflow.com/questions/50344232/not-able-to-set-multiple-events-in-chaincode-per-transaction-getting-only-last
	event := TuplesEvent{}
	event.SetTraintuples(out)

	err = SetEvent(stub, "tuples-updated", event)
	if err != nil {
		return nil, err
	}

	return map[string]string{"key": traintupleKey}, nil
}

// createTesttuple adds a Testtuple in the ledger
func createTesttuple(stub shim.ChaincodeStubInterface, args []string) (map[string]string, error) {
	inp := inputTesttuple{}
	err := AssetFromJSON(args, &inp)
	if err != nil {
		return nil, err
	}

	// check validity of input arg and set testtuple
	testtuple := Testtuple{}
	err = testtuple.SetFromTraintuple(stub, inp.TraintupleKey)
	if err != nil {
		return nil, err
	}
	err = testtuple.SetFromInput(stub, inp)
	if err != nil {
		return nil, err
	}
	testtupleKey, err := testtuple.GetKey(stub)
	if err != nil {
		return nil, errors.Conflict(err)
	}
	err = testtuple.Save(stub, testtupleKey)
	if err != nil {
		return nil, err
	}
	out := outputTesttuple{}
	err = out.Fill(stub, testtupleKey, testtuple)
	if err != nil {
		return nil, err
	}
	// https://github.com/hyperledger/fabric/blob/release-1.4/core/chaincode/shim/interfaces.go#L339:L343
	// We can only send one event per transaction
	// https://stackoverflow.com/questions/50344232/not-able-to-set-multiple-events-in-chaincode-per-transaction-getting-only-last
	event := TuplesEvent{}
	event.SetTesttuples(out)

	err = SetEvent(stub, "tuples-updated", event)
	if err != nil {
		return nil, err
	}

	return map[string]string{"key": testtupleKey}, nil
}

// logStartTrain modifies a traintuple by changing its status from todo to doing
func logStartTrain(stub shim.ChaincodeStubInterface, args []string) (outputTraintuple outputTraintuple, err error) {
	inp := inputHash{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}

	// get traintuple, check validity of the update
	traintuple := Traintuple{}
	if err = getElementStruct(stub, inp.Key, &traintuple); err != nil {
		return
	}
	if err = validateTupleOwner(stub, traintuple.Dataset.Worker); err != nil {
		return
	}
	if err = traintuple.commitStatusUpdate(stub, inp.Key, StatusDoing); err != nil {
		return
	}
	outputTraintuple.Fill(stub, traintuple, inp.Key)
	return
}

// logStartTest modifies a testtuple by changing its status from todo to doing
func logStartTest(stub shim.ChaincodeStubInterface, args []string) (outputTesttuple outputTesttuple, err error) {
	inp := inputHash{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}

	// get testtuple, check validity of the update, and update its status
	testtuple := Testtuple{}
	if err = getElementStruct(stub, inp.Key, &testtuple); err != nil {
		return
	}
	if err = validateTupleOwner(stub, testtuple.Dataset.Worker); err != nil {
		return
	}
	if err = testtuple.commitStatusUpdate(stub, inp.Key, StatusDoing); err != nil {
		return
	}
	err = outputTesttuple.Fill(stub, inp.Key, testtuple)
	if err != nil {
		return
	}
	return
}

// logSuccessTrain modifies a traintuple by changing its status from doing to done
// reports logs and associated performances
func logSuccessTrain(stub shim.ChaincodeStubInterface, args []string) (outputTraintuple outputTraintuple, err error) {
	inp := inputLogSuccessTrain{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}
	traintupleKey := inp.Key

	// get, update and commit traintuple
	traintuple := Traintuple{}
	if err = getElementStruct(stub, traintupleKey, &traintuple); err != nil {
		return
	}
	traintuple.Perf = inp.Perf
	traintuple.OutModel = &HashDress{
		Hash:           inp.OutModel.Hash,
		StorageAddress: inp.OutModel.StorageAddress}
	traintuple.Log += inp.Log

	if err = validateTupleOwner(stub, traintuple.Dataset.Worker); err != nil {
		return
	}
	if err = traintuple.commitStatusUpdate(stub, traintupleKey, StatusDone); err != nil {
		return
	}

	// update depending tuples
	traintuplesEvent, err := traintuple.updateTraintupleChildren(stub, traintupleKey)
	if err != nil {
		return
	}

	testtuplesEvent, err := traintuple.updateTesttupleChildren(stub, traintupleKey)
	if err != nil {
		return
	}

	outputTraintuple.Fill(stub, traintuple, inp.Key)

	// https://github.com/hyperledger/fabric/blob/release-1.4/core/chaincode/shim/interfaces.go#L339:L343
	// We can only send one event per transaction
	// https://stackoverflow.com/questions/50344232/not-able-to-set-multiple-events-in-chaincode-per-transaction-getting-only-last
	event := TuplesEvent{}
	event.SetTraintuples(traintuplesEvent...)
	event.SetTesttuples(testtuplesEvent...)

	err = SetEvent(stub, "tuples-updated", event)
	if err != nil {
		return
	}

	return
}

// logSuccessTest modifies a testtuple by changing its status to done, reports perf and logs
func logSuccessTest(stub shim.ChaincodeStubInterface, args []string) (outputTesttuple outputTesttuple, err error) {
	inp := inputLogSuccessTest{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}

	testtuple := Testtuple{}
	if err = getElementStruct(stub, inp.Key, &testtuple); err != nil {
		return
	}

	testtuple.Dataset.Perf = inp.Perf
	testtuple.Log += inp.Log

	if err = validateTupleOwner(stub, testtuple.Dataset.Worker); err != nil {
		return
	}
	if err = testtuple.commitStatusUpdate(stub, inp.Key, StatusDone); err != nil {
		return
	}
	err = outputTesttuple.Fill(stub, inp.Key, testtuple)
	return
}

// logFailTrain modifies a traintuple by changing its status to fail and reports associated logs
func logFailTrain(stub shim.ChaincodeStubInterface, args []string) (outputTraintuple outputTraintuple, err error) {
	inp := inputLogFailTrain{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}

	// get, update and commit traintuple
	traintuple := Traintuple{}
	if err = getElementStruct(stub, inp.Key, &traintuple); err != nil {
		return
	}
	traintuple.Log += inp.Log

	if err = validateTupleOwner(stub, traintuple.Dataset.Worker); err != nil {
		return
	}
	if err = traintuple.commitStatusUpdate(stub, inp.Key, StatusFailed); err != nil {
		return
	}

	outputTraintuple.Fill(stub, traintuple, inp.Key)

	// update depending tuples
	testtuplesEvent, err := traintuple.updateTesttupleChildren(stub, inp.Key)
	if err != nil {
		return
	}

	traintuplesEvent, err := traintuple.updateTraintupleChildren(stub, inp.Key)
	if err != nil {
		return
	}

	// https://github.com/hyperledger/fabric/blob/release-1.4/core/chaincode/shim/interfaces.go#L339:L343
	// We can only send one event per transaction
	// https://stackoverflow.com/questions/50344232/not-able-to-set-multiple-events-in-chaincode-per-transaction-getting-only-last
	event := TuplesEvent{}
	event.SetTraintuples(traintuplesEvent...)
	event.SetTesttuples(testtuplesEvent...)

	err = SetEvent(stub, "tuples-updated", event)
	if err != nil {
		return
	}

	return
}

// logFailTest modifies a testtuple by changing its status to fail and reports associated logs
func logFailTest(stub shim.ChaincodeStubInterface, args []string) (outputTesttuple outputTesttuple, err error) {
	inp := inputLogFailTest{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}

	// get, update and commit testtuple
	testtuple := Testtuple{}
	if err = getElementStruct(stub, inp.Key, &testtuple); err != nil {
		return
	}

	testtuple.Log += inp.Log

	if err = validateTupleOwner(stub, testtuple.Dataset.Worker); err != nil {
		return
	}
	if err = testtuple.commitStatusUpdate(stub, inp.Key, StatusFailed); err != nil {
		return
	}
	err = outputTesttuple.Fill(stub, inp.Key, testtuple)
	return
}

// queryTraintuple returns info about a traintuple given its key
func queryTraintuple(stub shim.ChaincodeStubInterface, args []string) (outputTraintuple outputTraintuple, err error) {
	inp := inputHash{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}
	traintuple := Traintuple{}
	if err = getElementStruct(stub, inp.Key, &traintuple); err != nil {
		return
	}
	if traintuple.AssetType != TraintupleType {
		err = errors.NotFound("no element with key %s", inp.Key)
		return
	}
	outputTraintuple.Fill(stub, traintuple, inp.Key)
	return
}

// queryTraintuples returns all traintuples
func queryTraintuples(stub shim.ChaincodeStubInterface, args []string) (outTraintuples []outputTraintuple, err error) {
	outTraintuples = []outputTraintuple{}

	if len(args) != 0 {
		err = errors.BadRequest("incorrect number of arguments, expecting nothing")
		return
	}
	elementsKeys, err := getKeysFromComposite(stub, "traintuple~algo~key", []string{"traintuple"})
	if err != nil {
		return
	}
	for _, key := range elementsKeys {
		var outputTraintuple outputTraintuple
		outputTraintuple, err = getOutputTraintuple(stub, key)
		if err != nil {
			return
		}
		outTraintuples = append(outTraintuples, outputTraintuple)
	}
	return
}

// queryTesttuple returns a testtuple of the ledger given its key
func queryTesttuple(stub shim.ChaincodeStubInterface, args []string) (out outputTesttuple, err error) {
	inp := inputHash{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}
	var testtuple Testtuple
	if err = getElementStruct(stub, inp.Key, &testtuple); err != nil {
		return
	}
	if testtuple.AssetType != TesttupleType {
		err = errors.NotFound("no element with key %s", inp.Key)
		return
	}
	err = out.Fill(stub, inp.Key, testtuple)
	return
}

// queryTesttuples returns all testtuples of the ledger
func queryTesttuples(stub shim.ChaincodeStubInterface, args []string) (outTesttuples []outputTesttuple, err error) {
	outTesttuples = []outputTesttuple{}

	if len(args) != 0 {
		err = errors.BadRequest("incorrect number of arguments, expecting nothing")
		return
	}
	var indexName = "testtuple~traintuple~certified~key"
	elementsKeys, err := getKeysFromComposite(stub, indexName, []string{"testtuple"})
	if err != nil {
		err = fmt.Errorf("issue getting keys from composite key %s - %s", indexName, err.Error())
		return
	}
	for _, key := range elementsKeys {
		var out outputTesttuple
		out, err = getOutputTesttuple(stub, key)
		if err != nil {
			return
		}
		outTesttuples = append(outTesttuples, out)
	}
	return
}

// queryModelDetails returns info about the testtuple and algo related to a traintuple
func queryModelDetails(stub shim.ChaincodeStubInterface, args []string) (outModelDetails outputModelDetails, err error) {
	inp := inputHash{}
	err = AssetFromJSON(args, &inp)
	if err != nil {
		return
	}

	// get associated traintuple
	outModelDetails.Traintuple, err = getOutputTraintuple(stub, inp.Key)
	if err != nil {
		return
	}

	// get certified and non-certified testtuples related to traintuple
	testtupleKeys, err := getKeysFromComposite(stub, "testtuple~traintuple~certified~key", []string{"testtuple", inp.Key})
	if err != nil {
		return
	}
	for _, testtupleKey := range testtupleKeys {
		// get testtuple and serialize it
		var outputTesttuple outputTesttuple
		outputTesttuple, err = getOutputTesttuple(stub, testtupleKey)
		if err != nil {
			return
		}

		if outputTesttuple.Certified {
			outModelDetails.Testtuple = outputTesttuple
		} else {
			outModelDetails.NonCertifiedTesttuples = append(outModelDetails.NonCertifiedTesttuples, outputTesttuple)
		}
	}
	return
}

// queryModels returns all traintuples and associated testuples
func queryModels(stub shim.ChaincodeStubInterface, args []string) (outModels []outputModel, err error) {
	outModels = []outputModel{}

	if len(args) != 0 {
		err = errors.BadRequest("incorrect number of arguments, expecting nothing")
		return
	}

	traintupleKeys, err := getKeysFromComposite(stub, "traintuple~algo~key", []string{"traintuple"})
	if err != nil {
		return
	}
	for _, traintupleKey := range traintupleKeys {
		var outputModel outputModel

		// get traintuple
		outputModel.Traintuple, err = getOutputTraintuple(stub, traintupleKey)
		if err != nil {
			return
		}

		// get associated testtuple
		var testtupleKeys []string
		testtupleKeys, err = getKeysFromComposite(stub, "testtuple~traintuple~certified~key", []string{"testtuple", traintupleKey, "true"})
		if err != nil {
			return
		}
		if len(testtupleKeys) == 1 {
			// get testtuple and serialize it
			testtupleKey := testtupleKeys[0]
			outputModel.Testtuple, err = getOutputTesttuple(stub, testtupleKey)
			if err != nil {
				return
			}
		}
		outModels = append(outModels, outputModel)
	}
	return
}

// --------------------------------------------------------------
// Utils for smartcontracts related to traintuples and testtuples
// --------------------------------------------------------------

// getOutputTraintuple takes as input a traintuple key and returns the outputTraintuple
func getOutputTraintuple(stub shim.ChaincodeStubInterface, traintupleKey string) (outTraintuple outputTraintuple, err error) {
	traintuple := Traintuple{}
	if err = getElementStruct(stub, traintupleKey, &traintuple); err != nil {
		return
	}
	outTraintuple.Fill(stub, traintuple, traintupleKey)
	return
}

// getOutputTraintuples takes as input a list of keys and returns a paylaod containing a list of associated retrieved elements
func getOutputTraintuples(stub shim.ChaincodeStubInterface, traintupleKeys []string) (outTraintuples []outputTraintuple, err error) {
	for _, key := range traintupleKeys {
		var outputTraintuple outputTraintuple
		outputTraintuple, err = getOutputTraintuple(stub, key)
		if err != nil {
			return
		}
		outTraintuples = append(outTraintuples, outputTraintuple)
	}
	return
}

// getOutputTesttuple takes as input a testtuple key and returns the outputTesttuple
func getOutputTesttuple(stub shim.ChaincodeStubInterface, testtupleKey string) (outTesttuple outputTesttuple, err error) {
	testtuple := Testtuple{}
	if err = getElementStruct(stub, testtupleKey, &testtuple); err != nil {
		return
	}
	err = outTesttuple.Fill(stub, testtupleKey, testtuple)
	return
}

// getOutputTesttuples takes as input a list of keys and returns a paylaod containing a list of associated retrieved elements
func getOutputTesttuples(stub shim.ChaincodeStubInterface, testtupleKeys []string) (outTesttuples []outputTesttuple, err error) {
	for _, key := range testtupleKeys {
		var outputTesttuple outputTesttuple
		outputTesttuple, err = getOutputTesttuple(stub, key)
		if err != nil {
			return
		}
		outTesttuples = append(outTesttuples, outputTesttuple)
	}
	return
}

// checkLog checks the validity of logs
func checkLog(log string) (err error) {
	maxLength := 200
	if length := len(log); length > maxLength {
		err = fmt.Errorf("too long log, is %d and should be %d ", length, maxLength)
	}
	return
}

func validateTupleOwner(stub shim.ChaincodeStubInterface, worker string) error {
	txCreator, err := getTxCreator(stub)
	if err != nil {
		return err
	}
	if txCreator != worker {
		return fmt.Errorf("%s is not allowed to update tuple (%s)", txCreator, worker)
	}
	return nil
}

// check validity of traintuple update: consistent status and agent submitting the transaction
func checkUpdateTuple(stub shim.ChaincodeStubInterface, worker string, oldStatus string, newStatus string) error {
	statusPossibilities := map[string]string{
		StatusWaiting: StatusTodo,
		StatusTodo:    StatusDoing,
		StatusDoing:   StatusDone}
	if statusPossibilities[oldStatus] != newStatus && newStatus != StatusFailed {
		return errors.BadRequest("cannot change status from %s to %s", oldStatus, newStatus)
	}
	return nil
}

// validateNewStatus verifies that the new status is consistent with the tuple current status
func (traintuple *Traintuple) validateNewStatus(stub shim.ChaincodeStubInterface, status string) error {
	// check validity of worker and change of status
	if err := checkUpdateTuple(stub, traintuple.Dataset.Worker, traintuple.Status, status); err != nil {
		return err
	}
	return nil
}

// validateNewStatus verifies that the new status is consistent with the tuple current status
func (testtuple *Testtuple) validateNewStatus(stub shim.ChaincodeStubInterface, status string) error {
	// check validity of worker and change of status
	if err := checkUpdateTuple(stub, testtuple.Dataset.Worker, testtuple.Status, status); err != nil {
		return err
	}
	return nil
}

// updateTraintupleChildren updates the status of waiting trainuples  InModels of traintuples once they have been trained (succesfully or failed)
func (traintuple *Traintuple) updateTraintupleChildren(stub shim.ChaincodeStubInterface, traintupleKey string) ([]outputTraintuple, error) {

	// tuples to be sent in event
	otuples := []outputTraintuple{}

	// get traintuples having as inModels the input traintuple
	indexName := "traintuple~inModel~key"
	childTraintupleKeys, err := getKeysFromComposite(stub, indexName, []string{"traintuple", traintupleKey})
	if err != nil {
		return otuples, fmt.Errorf("error while getting associated traintuples to update their inModel")
	}
	for _, childTraintupleKey := range childTraintupleKeys {
		// get and update traintuple
		childTraintuple := Traintuple{}
		if err := getElementStruct(stub, childTraintupleKey, &childTraintuple); err != nil {
			return otuples, err
		}

		// remove associated composite key
		if err := childTraintuple.removeModelCompositeKey(stub, traintupleKey); err != nil {
			return otuples, err
		}

		// traintuple is already failed, don't update it
		if childTraintuple.Status == StatusFailed {
			continue
		}

		if childTraintuple.Status != StatusWaiting {
			return otuples, fmt.Errorf("traintuple %s has invalid status : '%s' instead of waiting", childTraintupleKey, childTraintuple.Status)
		}

		// get traintuple new status
		var newStatus string
		if traintuple.Status == StatusFailed {
			newStatus = StatusFailed
		} else if traintuple.Status == StatusDone {
			ready, err := childTraintuple.isReady(stub, traintupleKey)
			if err != nil {
				return otuples, err
			}
			if ready {
				newStatus = StatusTodo
			}
		}

		// commit new status
		if newStatus == "" {
			continue
		}
		if err := childTraintuple.commitStatusUpdate(stub, childTraintupleKey, newStatus); err != nil {
			return otuples, err
		}
		if newStatus == StatusTodo {
			out := outputTraintuple{}
			err = out.Fill(stub, childTraintuple, childTraintupleKey)
			if err != nil {
				return otuples, err
			}
			otuples = append(otuples, out)
		}
	}
	return otuples, nil
}

// isReady checks if inModels of a traintuple have been trained, except the newDoneTraintupleKey (since the transaction is not commited)
// and updates the traintuple status if necessary
func (traintuple *Traintuple) isReady(stub shim.ChaincodeStubInterface, newDoneTraintupleKey string) (ready bool, err error) {
	for _, key := range traintuple.InModelKeys {
		// don't check newly done traintuple
		if key == newDoneTraintupleKey {
			continue
		}
		tt := Traintuple{}
		if err := getElementStruct(stub, key, &tt); err != nil {
			return false, err
		}
		if tt.Status != StatusDone {
			return false, nil
		}
	}
	return true, nil
}

// removeModelCompositeKey remove the Model key state of a traintuple
func (traintuple *Traintuple) removeModelCompositeKey(stub shim.ChaincodeStubInterface, modelKey string) error {
	indexName := "traintuple~inModel~key"
	compositeKey, err := stub.CreateCompositeKey(indexName, []string{"traintuple", modelKey, traintuple.FLTask})

	if err != nil {
		return fmt.Errorf("failed to recreate composite key to update traintuple %s with inModel %s - %s", traintuple.FLTask, modelKey, err.Error())
	}

	if err := stub.DelState(compositeKey); err != nil {
		return fmt.Errorf("failed to delete associated composite key to update traintuple %s with inModel %s - %s", traintuple.FLTask, modelKey, err.Error())
	}
	return nil
}

// commitStatusUpdate update the traintuple status in the ledger
func (traintuple *Traintuple) commitStatusUpdate(stub shim.ChaincodeStubInterface, traintupleKey string, newStatus string) error {
	if traintuple.Status == newStatus {
		return fmt.Errorf("cannot update traintuple %s - status already %s", traintupleKey, newStatus)
	}

	if err := traintuple.validateNewStatus(stub, newStatus); err != nil {
		return fmt.Errorf("update traintuple %s failed: %s", traintupleKey, err.Error())
	}

	oldStatus := traintuple.Status
	traintuple.Status = newStatus
	traintupleBytes, _ := json.Marshal(traintuple)
	if err := stub.PutState(traintupleKey, traintupleBytes); err != nil {
		return fmt.Errorf("failed to update traintuple %s - %s", traintupleKey, err.Error())
	}

	// update associated composite keys
	indexName := "traintuple~worker~status~key"
	oldAttributes := []string{"traintuple", traintuple.Dataset.Worker, oldStatus, traintupleKey}
	newAttributes := []string{"traintuple", traintuple.Dataset.Worker, traintuple.Status, traintupleKey}
	if err := updateCompositeKey(stub, indexName, oldAttributes, newAttributes); err != nil {
		return err
	}
	logger.Infof("traintuple %s status updated: %s (from=%s)", traintupleKey, newStatus, oldStatus)
	return nil
}

// updateTesttupleChildren update testtuples status associated with a done or failed traintuple
func (traintuple *Traintuple) updateTesttupleChildren(stub shim.ChaincodeStubInterface, traintupleKey string) ([]outputTesttuple, error) {

	otuples := []outputTesttuple{}

	var newStatus string
	if traintuple.Status == StatusFailed {
		newStatus = StatusFailed
	} else if traintuple.Status == StatusDone {
		newStatus = StatusTodo
	} else {
		return otuples, nil
	}

	indexName := "testtuple~traintuple~certified~key"
	// get testtuple associated with this traintuple and updates its status
	testtupleKeys, err := getKeysFromComposite(stub, indexName, []string{"testtuple", traintupleKey})
	if err != nil {
		return otuples, err
	}
	for _, testtupleKey := range testtupleKeys {
		// get and update testtuple
		testtuple := Testtuple{}
		if err := getElementStruct(stub, testtupleKey, &testtuple); err != nil {
			return otuples, err
		}
		testtuple.Model = &Model{
			TraintupleKey: traintupleKey,
		}

		if newStatus == StatusTodo {
			testtuple.Model.Hash = traintuple.OutModel.Hash
			testtuple.Model.StorageAddress = traintuple.OutModel.StorageAddress
		}

		if err := testtuple.commitStatusUpdate(stub, testtupleKey, newStatus); err != nil {
			return otuples, err
		}

		if newStatus == StatusTodo {
			out := outputTesttuple{}
			err = out.Fill(stub, testtupleKey, testtuple)
			if err != nil {
				return nil, err
			}
			otuples = append(otuples, out)
		}
	}
	return otuples, nil
}

// commitStatusUpdate update the testtuple status in the ledger
func (testtuple *Testtuple) commitStatusUpdate(stub shim.ChaincodeStubInterface, testtupleKey string, newStatus string) error {
	if err := testtuple.validateNewStatus(stub, newStatus); err != nil {
		return fmt.Errorf("update testtuple %s failed: %s", testtupleKey, err.Error())
	}

	oldStatus := testtuple.Status
	testtuple.Status = newStatus

	testtupleBytes, _ := json.Marshal(testtuple)
	if err := stub.PutState(testtupleKey, testtupleBytes); err != nil {
		return fmt.Errorf("failed to update testtuple status to %s with key %s", newStatus, testtupleKey)
	}

	// update associated composite key
	indexName := "testtuple~worker~status~key"
	oldAttributes := []string{"testtuple", testtuple.Dataset.Worker, oldStatus, testtupleKey}
	newAttributes := []string{"testtuple", testtuple.Dataset.Worker, testtuple.Status, testtupleKey}
	if err := updateCompositeKey(stub, indexName, oldAttributes, newAttributes); err != nil {
		return err
	}
	logger.Infof("testtuple %s status updated: %s (from=%s)", testtupleKey, newStatus, oldStatus)
	return nil
}

// getTraintuplesPayload takes as input a list of keys and returns a paylaod containing a list of associated retrieved elements
func getTraintuplesPayload(stub shim.ChaincodeStubInterface, traintupleKeys []string) ([]map[string]interface{}, error) {

	var elements []map[string]interface{}
	for _, key := range traintupleKeys {
		var element map[string]interface{}
		outputTraintuple, err := getOutputTraintuple(stub, key)
		if err != nil {
			return nil, err
		}
		oo, err := json.Marshal(outputTraintuple)
		if err != nil {
			return nil, err
		}
		json.Unmarshal(oo, &element)
		element["key"] = key
		elements = append(elements, element)
	}
	return elements, nil
}

// HashForKey to generate key for an asset
func HashForKey(stub shim.ChaincodeStubInterface, objectType string, hashElements ...string) (key string, err error) {
	toHash := objectType
	sort.Strings(hashElements)
	for _, element := range hashElements {
		toHash += "," + element
	}
	sum := sha256.Sum256([]byte(toHash))
	key = hex.EncodeToString(sum[:])
	if bytes, stubErr := stub.GetState(key); bytes != nil {
		err = fmt.Errorf("this %s already exists (tkey: %s)", objectType, key)
	} else if stubErr != nil {
		return key, stubErr
	}
	return
}
