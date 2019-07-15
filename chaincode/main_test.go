package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hyperledger/fabric/core/chaincode/shim"
	peer "github.com/hyperledger/fabric/protos/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const objectiveDescriptionHash = "5c1d9cd1c2c1082dde0921b56d11030c81f62fbb51932758b58ac2569dd0b379"
const objectiveDescriptionStorageAddress = "https://toto/objective/222/description"
const objectiveMetricsHash = "4a1d9cd1c2c1082dde0921b56d11030c81f62fbb51932758b58ac2569dd0b379"
const objectiveMetricsStorageAddress = "https://toto/objective/222/metrics"
const dataManagerOpenerHash = "da1bb7c31f62244c0f3a761cc168804227115793d01c270021fe3f7935482dcc"
const trainDataSampleHash1 = "aa1bb7c31f62244c0f3a761cc168804227115793d01c270021fe3f7935482dcc"
const trainDataSampleHash2 = "aa2bb7c31f62244c0f3a761cc168804227115793d01c270021fe3f7935482dcc"
const testDataSampleHash1 = "bb1bb7c31f62244c0f3a761cc168804227115793d01c270021fe3f7935482dcc"
const testDataSampleHash2 = "bb2bb7c31f62244c0f3a761cc168804227115793d01c270021fe3f7935482dcc"
const algoHash = "fd1bb7c31f62244c0f3a761cc168804227115793d01c270021fe3f7935482dcc"
const algoStorageAddress = "https://toto/algo/222/algo"
const algoName = "hog + svm"
const modelHash = "eedbb7c31f62244c0f3a761cc168804227115793d01c270021fe3f7935482eed"
const modelAddress = "https://substrabac/model/toto"
const worker = "bbd157aa8e85eb985aeedb79361cd45739c92494dce44d351fd2dbd6190e27f0"
const traintupleKey = "8e29bacef1250f8c3bd6ccc72455f764b74ef7e66b9157fd6cd2b0cecef1c687"

var (
	pipeline = flag.Bool("pipeline", false, "Print out the pipeline test output")
	readme   = flag.String("readme", "../README.md", "Pass the path to the README and compare it to the output")
	update   = flag.Bool("update", false, "Update the README.md file")
)

func TestInit(t *testing.T) {
	scc := new(SubstraChaincode)
	mockStub := shim.NewMockStub("substra", scc)

	// resp := mockStub.MockInit("42", [][]byte{[]byte("init")})
	resp := mockStub.MockInit("42", [][]byte{[]byte("init")})
	assert.EqualValuesf(t, 200, resp.Status, "init failed with status %d and message %s", resp.Status, resp.Message)
}

func methodToByte(methodName string) [][]byte {
	return [][]byte{[]byte(methodName)}
}

func methodAndAssetToByte(methodName string, asset interface{}) [][]byte {
	return [][]byte{[]byte(methodName), assetToJSON(asset)}
}

func methodAndKeyToByte(key string, asset interface{}) [][]byte {
	return [][]byte{[]byte("queryAlgo"), keyToJSON(key)}
}

func assetToArgs(asset interface{}) []string {
	return []string{string(assetToJSON(asset))}
}

func assetToJSON(asset interface{}) []byte {
	assetjson, _ := json.Marshal(asset)
	return assetjson
}

func keyToJSON(key string) []byte {
	return assetToJSON(inputHash{Key: key})
}
func printArgs(buf io.Writer, args [][]byte, command string) {
	fmt.Fprintln(buf, "##### Command peer example:")
	fmt.Fprintf(buf, "```bash\npeer chaincode %s -n mycc -c '{\"Args\":[\"%s\"", command, args[0])
	if len(args) == 2 {
		escapedJSON, _ := json.Marshal(string(args[1]))
		fmt.Fprintf(buf, ",%s", escapedJSON)
	}
	fmt.Fprint(buf, "]}' -C myc\n```\n")
}

func prettyPrintStruct(buf io.Writer, margin string, strucType reflect.Type) {
	fmt.Fprintln(buf, "{")
	prettyPrintStructElements(buf, margin+" ", strucType)
	fmt.Fprintf(buf, "%s}\n", margin)
}
func prettyPrintStructElements(buf io.Writer, margin string, strucType reflect.Type) {
	for i := 0; i < strucType.NumField(); i++ {
		f := strucType.Field(i)
		if f.Type.Kind() == reflect.Struct {
			if f.Anonymous {
				prettyPrintStructElements(buf, margin, f.Type)
			} else {
				fmt.Fprintf(buf, "%s\"%s\": (%s)", margin, f.Tag.Get("json"), f.Tag.Get("validate"))
				prettyPrintStruct(buf, margin+"  ", f.Type)
				fmt.Fprint(buf, margin)
			}
			continue
		}
		fieldType := f.Type.Kind()
		fieldStr := ""
		if fieldType == reflect.Slice && f.Type.Elem().Kind() == reflect.Struct {
			fmt.Fprintf(buf, "%s\"%s\": (%s) [\n%s", margin, f.Tag.Get("json"), f.Tag.Get("validate"), margin+"  ")
			prettyPrintStruct(buf, margin+"  ", f.Type.Elem())
			fmt.Fprintf(buf, "%s],\n", margin)
			continue
		} else if fieldType == reflect.Slice {
			fieldStr = fmt.Sprintf("[%s]", f.Type.Elem().Kind())
		} else {
			fieldStr = fmt.Sprint(fieldType)
		}
		fmt.Fprintf(buf, "%s\"%s\": %s (%s),\n", margin, f.Tag.Get("json"), fieldStr, f.Tag.Get("validate"))
	}
}

func printInputStuct(buf io.Writer, fnName string, inputType reflect.Type) {
	fmt.Fprintf(buf, "Smart contract: `%s`\n\n##### JSON Inputs:\n```go\n", fnName) // ", fnName)
	prettyPrintStruct(buf, "", inputType)
	fmt.Fprint(buf, "```\n")
}

func registerItem(t *testing.T, mockStub shim.MockStub, itemType string) (peer.Response, interface{}) {
	// 1. add dataManager
	inpDataManager := inputDataManager{}
	args := inpDataManager.createDefault()
	resp := mockStub.MockInvoke("42", args)
	require.EqualValuesf(t, 200, resp.Status, "when adding dataManager with status %d and message %s", resp.Status, resp.Message)
	if itemType == "dataManager" {
		return resp, inpDataManager
	}
	// 2. add test dataSample
	inpDataSample := inputDataSample{
		Hashes:          testDataSampleHash1 + ", " + testDataSampleHash2,
		DataManagerKeys: []string{dataManagerOpenerHash},
		TestOnly:        "true",
	}
	args = inpDataSample.createDefault()
	resp = mockStub.MockInvoke("42", args)
	require.EqualValuesf(t, 200, resp.Status, "when adding test dataSample with status %d and message %s", resp.Status, resp.Message)
	if itemType == "testDataset" {
		return resp, inpDataSample
	}
	// 3. add objective
	inpObjective := inputObjective{}
	args = inpObjective.createDefault()
	resp = mockStub.MockInvoke("42", args)
	require.EqualValuesf(t, 200, resp.Status, "when adding objective with status %d and message %s", resp.Status, resp.Message)
	if itemType == "objective" {
		return resp, inpObjective
	}
	// 4. Add train dataSample
	inpDataSample = inputDataSample{}
	args = inpDataSample.createDefault()
	resp = mockStub.MockInvoke("42", args)
	require.EqualValuesf(t, 200, resp.Status, "when adding train dataSample with status %d and message %s", resp.Status, resp.Message)
	if itemType == "trainDataset" {
		return resp, inpDataSample
	}
	// 5. Add algo
	inpAlgo := inputAlgo{}
	args = inpAlgo.createDefault()
	resp = mockStub.MockInvoke("42", args)
	require.EqualValuesf(t, 200, resp.Status, "when adding algo with status %d and message %s", resp.Status, resp.Message)
	if itemType == "algo" {
		return resp, inpAlgo
	}
	// 6. Add traintuple
	inpTraintuple := inputTraintuple{}
	args = inpTraintuple.createDefault()
	resp = mockStub.MockInvoke("42", args)
	require.EqualValuesf(t, 200, resp.Status, "when adding traintuple with status %d and message %s", resp.Status, resp.Message)
	return resp, inpTraintuple
}

func printResp(buf io.Writer, payload []byte) {
	var toPrint []byte
	if strings.HasPrefix(string(payload), "{") {
		obj := map[string]interface{}{}
		json.Unmarshal(payload, &obj)
		toPrint, _ = json.MarshalIndent(obj, "", " ")
	} else if strings.HasPrefix(string(payload), "[") {
		obj := []map[string]interface{}{}
		json.Unmarshal(payload, &obj)
		toPrint, _ = json.MarshalIndent(obj, "", " ")
	} else {
		toPrint = payload
	}
	fmt.Fprintf(buf, "##### Command output:\n```json\n%s\n```\n", toPrint)
}

func TestPipeline(t *testing.T) {
	scc := new(SubstraChaincode)
	mockStub := shim.NewMockStub("substra", scc)
	var out strings.Builder
	callAssertAndPrint := func(peerCmd, smartContract string, inputAsset interface{}) peer.Response {
		var args [][]byte
		if inputAsset != nil {
			printInputStuct(&out, smartContract, reflect.TypeOf(inputAsset))
			args = methodAndAssetToByte(smartContract, inputAsset)
		} else {
			args = methodToByte(smartContract)
		}
		printArgs(&out, args, peerCmd)
		resp := mockStub.MockInvoke("42", args)
		require.EqualValuesf(t, 200, resp.Status, "problem when calling %s, return status %d and message %s", smartContract, resp.Status, resp.Message)
		printResp(&out, resp.Payload)
		return resp
	}

	fmt.Fprintln(&out, "#### ------------ Add DataManager ------------")
	inpDataManager := inputDataManager{}
	inpDataManager.createDefault()
	resp := callAssertAndPrint("invoke", "registerDataManager", inpDataManager)
	// Get dataManager key from Payload
	res := map[string]string{}
	err := json.Unmarshal(resp.Payload, &res)
	assert.NoError(t, err, "should unmarshal without problem")
	assert.Contains(t, res, "key")
	dataManagerKey := res["key"]

	fmt.Fprintln(&out, "#### ------------ Query DataManager From key ------------")
	callAssertAndPrint("invoke", "queryDataManager", inputHash{dataManagerKey})

	fmt.Fprintln(&out, "#### ------------ Add test DataSample ------------")
	inpDataSample := inputDataSample{
		Hashes:   testDataSampleHash1 + ", " + testDataSampleHash2,
		TestOnly: "true",
	}
	inpDataSample.createDefault()
	callAssertAndPrint("invoke", "registerDataSample", inpDataSample)

	fmt.Fprintln(&out, "#### ------------ Add Objective ------------")
	inpObjective := inputObjective{}
	inpObjective.createDefault()
	callAssertAndPrint("invoke", "registerObjective", inpObjective)

	fmt.Fprintln(&out, "#### ------------ Add Algo ------------")
	inpAlgo := inputAlgo{}
	inpAlgo.createDefault()
	callAssertAndPrint("invoke", "registerAlgo", inpAlgo)

	fmt.Fprintln(&out, "#### ------------ Add Train DataSample ------------")
	inpDataSample = inputDataSample{}
	inpDataSample.createDefault()
	callAssertAndPrint("invoke", "registerDataSample", inpDataSample)

	fmt.Fprintln(&out, "#### ------------ Query DataManagers ------------")
	callAssertAndPrint("query", "queryDataManagers", nil)

	fmt.Fprintln(&out, "#### ------------ Query DataSamples ------------")
	callAssertAndPrint("query", "queryDataSamples", nil)

	fmt.Fprintln(&out, "#### ------------ Query Objectives ------------")
	callAssertAndPrint("query", "queryObjectives", nil)

	fmt.Fprintln(&out, "#### ------------ Add Traintuple ------------")
	inpTraintuple := inputTraintuple{}
	args := inpTraintuple.createDefault()
	resp = callAssertAndPrint("invoke", "createTraintuple", inpTraintuple)
	// Get traintuple key from Payload
	res = map[string]string{}
	err = json.Unmarshal(resp.Payload, &res)
	assert.NoError(t, err, "should unmarshal without problem")
	assert.Contains(t, res, "key")
	traintupleKey := res["key"]
	// check not possible to create same traintuple
	resp = mockStub.MockInvoke("42", args)
	assert.EqualValuesf(t, 409, resp.Status, "when adding same traintuple with status %d and message %s", resp.Status, resp.Message)
	// Get owner of the traintuple
	args = [][]byte{[]byte("queryTraintuple"), keyToJSON(traintupleKey)}
	resp = mockStub.MockInvoke("42", args)
	assert.EqualValuesf(t, 200, resp.Status, "when adding traintuple with status %d and message %s", resp.Status, resp.Message)
	traintuple := outputTraintuple{}
	respTraintuple := resp.Payload
	if err := bytesToStruct(respTraintuple, &traintuple); err != nil {
		t.Errorf("when unmarshalling queried traintuple with error %s", err)
	}
	trainWorker := traintuple.Dataset.Worker

	fmt.Fprintln(&out, "#### ------------ Add Traintuple with inModel from previous traintuple ------------")
	inpTraintuple = inputTraintuple{}
	inpTraintuple.InModels = []string{traintupleKey}
	inpTraintuple.createDefault()
	resp = callAssertAndPrint("invoke", "createTraintuple", inpTraintuple)
	printResp(&out, resp.Payload)
	res = map[string]string{}
	err = json.Unmarshal(resp.Payload, &res)
	assert.NoError(t, err, "should unmarshal without problem")
	assert.Contains(t, res, "key")
	todoTraintupleKey := res["key"]

	fmt.Fprintln(&out, "#### ------------ Query Traintuples of worker with todo status ------------")
	filter := inputQueryFilter{
		IndexName:  "traintuple~worker~status",
		Attributes: trainWorker + ", todo",
	}
	callAssertAndPrint("invoke", "queryFilter", filter)

	fmt.Fprintln(&out, "#### ------------ Log Start Training ------------")
	callAssertAndPrint("invoke", "logStartTrain", inputHash{traintupleKey})

	fmt.Fprintln(&out, "#### ------------ Log Success Training ------------")
	inp := inputLogSuccessTrain{}
	inp.Key = string(traintupleKey)
	inp.createDefault()
	callAssertAndPrint("invoke", "logSuccessTrain", inp)

	fmt.Fprintln(&out, "#### ------------ Query Traintuple From key ------------")
	callAssertAndPrint("invoke", "queryTraintuple", inputHash{traintupleKey})

	fmt.Fprintln(&out, "#### ------------ Add Non-Certified Testtuple ------------")
	inpTesttuple := inputTesttuple{
		DataManagerKey: dataManagerOpenerHash,
		DataSampleKeys: []string{trainDataSampleHash1, trainDataSampleHash2},
	}
	inpTesttuple.createDefault()
	callAssertAndPrint("invoke", "createTesttuple", inpTesttuple)

	fmt.Fprintln(&out, "#### ------------ Add Certified Testtuple ------------")
	inpTesttuple = inputTesttuple{}
	args = inpTesttuple.createDefault()
	resp = callAssertAndPrint("invoke", "createTesttuple", inpTesttuple)
	// Get testtuple key from Payload
	res = map[string]string{}
	err = json.Unmarshal(resp.Payload, &res)
	assert.NoError(t, err, "should unmarshal without problem")
	assert.Contains(t, res, "key")
	testtupleKey := res["key"]
	// check not possible to create same testtuple
	resp = mockStub.MockInvoke("42", args)
	assert.EqualValuesf(t, 409, resp.Status, "when adding same testtuple with status %d and message %s", resp.Status, resp.Message)
	// Get owner of the testtuple
	args = [][]byte{[]byte("queryTesttuple"), keyToJSON(testtupleKey)}
	resp = mockStub.MockInvoke("42", args)
	respTesttuple := resp.Payload
	testtuple := Testtuple{}
	if err := bytesToStruct(respTesttuple, &testtuple); err != nil {
		t.Errorf("when unmarshalling queried testtuple with error %s", err)
	}
	testWorker := testtuple.Dataset.Worker

	fmt.Fprintln(&out, "#### ------------ Add Testtuple with not done traintuple ------------")
	inpTesttuple = inputTesttuple{}
	inpTesttuple.TraintupleKey = todoTraintupleKey
	inpTesttuple.createDefault()
	callAssertAndPrint("invoke", "createTesttuple", inpTesttuple)

	fmt.Fprintln(&out, "#### ------------ Query Testtuples of worker with todo status ------------")
	filter = inputQueryFilter{
		IndexName:  "testtuple~worker~status",
		Attributes: testWorker + ", todo",
	}
	callAssertAndPrint("invoke", "queryFilter", filter)

	fmt.Fprintln(&out, "#### ------------ Log Start Testing ------------")
	callAssertAndPrint("invoke", "logStartTest", inputHash{testtupleKey})

	fmt.Fprintln(&out, "#### ------------ Log Success Testing ------------")
	success := inputLogSuccessTest{}
	success.Key = testtupleKey
	args = success.createDefault()
	callAssertAndPrint("invoke", "logSuccessTest", success)

	fmt.Fprintln(&out, "#### ------------ Query Testtuple from its key ------------")
	callAssertAndPrint("query", "queryTesttuple", inputHash{testtupleKey})

	fmt.Fprintln(&out, "#### ------------ Query all Testtuples ------------")
	callAssertAndPrint("query", "queryTesttuples", nil)

	fmt.Fprintln(&out, "#### ------------ Query details about a model ------------")
	callAssertAndPrint("query", "queryModelDetails", inputHash{traintupleKey})

	fmt.Fprintln(&out, "#### ------------ Query all models ------------")
	callAssertAndPrint("query", "queryModels", nil)

	fmt.Fprintln(&out, "#### ------------ Query Dataset ------------")
	callAssertAndPrint("query", "queryDataset", inputHash{dataManagerOpenerHash})

	// 3. add new data manager and dataSample
	fmt.Fprintln(&out, "#### ------------ Update Data Sample with new data manager ------------")
	newDataManagerKey := "38a320b2a67c8003cc748d6666534f2b01f3f08d175440537a5bf86b7d08d5ee"
	inpDataManager = inputDataManager{OpenerHash: newDataManagerKey}
	args = inpDataManager.createDefault()
	resp = mockStub.MockInvoke("42", args)
	assert.EqualValuesf(t, 200, resp.Status, "when adding dataManager with status %d and message %s", resp.Status, resp.Message)
	// associate a data sample with the old data manager with the updateDataSample
	updateData := inputUpdateDataSample{
		DataManagerKeys: []string{newDataManagerKey},
		Hashes:          trainDataSampleHash1,
	}
	callAssertAndPrint("invoke", "updateDataSample", updateData)

	fmt.Fprintln(&out, "#### ------------ Query the new Dataset ------------")
	callAssertAndPrint("query", "queryDataset", inputHash{newDataManagerKey})

	fmt.Fprintln(&out, "#### ------------ Create a ComputePlan ------------")
	callAssertAndPrint("invoke", "createComputePlan", defaultComputePlan)

	// Use the output to check the README file and if asked update it
	doc := out.String()
	fromFile, err := ioutil.ReadFile(*readme)
	require.NoErrorf(t, err, "can not read the readme file at the path %s", *readme)
	actualReadme := string(fromFile)
	exampleTitle := "### Examples\n\n"
	index := strings.Index(actualReadme, exampleTitle)
	require.NotEqual(t, -1, index, "README file does not include a Examples section")
	if *update {
		err = ioutil.WriteFile(*readme, []byte(actualReadme[:index+len(exampleTitle)]+doc), 0644)
		assert.NoError(t, err)
	} else {
		testUsage := "The Readme examples are not up to date with the tests"
		testUsage += "\n`-pipeline` to see the output"
		testUsage += "\n`-update` to update the README"
		testUsage += "\n`-readme` to set a different path for the README"
		assert.True(t, strings.Contains(actualReadme, doc), testUsage)
	}
	if *pipeline {
		fmt.Println(doc, index)
	}
}

func TestMain(m *testing.M) {
	//Raise log level to silence it during tests
	logger.SetLevel(shim.LogCritical)
	os.Exit(m.Run())
}

func initializeMockStateDB(t *testing.T, stub *shim.MockStub) {
	stub.MockTransactionStart("42")
	stub.PutState("key", []byte("value"))
}

func TestQueryEmptyResponse(t *testing.T) {
	scc := new(SubstraChaincode)
	mockStub := shim.NewMockStub("substra", scc)
	initializeMockStateDB(t, mockStub)

	smartContracts := []string{
		"queryAlgos",
		"queryDataSamples",
		"queryObjectives",
		"queryDataManagers",
		"queryTraintuples",
		"queryTesttuples",
		"queryModels",
	}

	for _, contractName := range smartContracts {
		t.Run(contractName, func(t *testing.T) {
			args := [][]byte{[]byte(contractName)}
			resp := mockStub.MockInvoke("42", args)

			expectedPayload, _ := json.Marshal(make([]string, 0))
			assert.Equal(t, expectedPayload, resp.Payload, "payload is not an empty list")
		})
	}
}
