package friggdb

import (
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/grafana/frigg/pkg/friggpb"
	"github.com/grafana/frigg/pkg/util/test"
)

func TestCreateBlock(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	wal, err := newWAL(&walConfig{
		filepath:        tempDir,
		indexDownsample: 2,
	})
	assert.NoError(t, err, "unexpected error creating temp wal")

	blockID := uuid.New()
	tenantID := "fake"

	block, err := wal.NewBlock(blockID, tenantID)
	assert.NoError(t, err, "unexpected error creating block")

	blocks, err := wal.AllBlocks()
	assert.NoError(t, err, "unexpected error getting blocks")
	assert.Len(t, blocks, 1)

	assert.Equal(t, block.(*headBlock).fullFilename(), blocks[0].(*headBlock).fullFilename())
}

func TestReadWrite(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	wal, err := newWAL(&walConfig{
		filepath:        tempDir,
		indexDownsample: 2,
	})
	assert.NoError(t, err, "unexpected error creating temp wal")

	blockID := uuid.New()
	tenantID := "fake"

	block, err := wal.NewBlock(blockID, tenantID)
	assert.NoError(t, err, "unexpected error creating block")

	req := test.MakeRequest(10, []byte{0x00, 0x01})
	err = block.Write([]byte{0x00, 0x01}, req)
	assert.NoError(t, err, "unexpected error creating writing req")

	outReq := &friggpb.PushRequest{}
	found, err := block.Find([]byte{0x00, 0x01}, outReq)
	assert.NoError(t, err, "unexpected error creating reading req")
	assert.True(t, found)
	assert.True(t, proto.Equal(req, outReq))
}

func TestIterator(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	wal, err := newWAL(&walConfig{
		filepath:        tempDir,
		indexDownsample: 2,
	})
	assert.NoError(t, err, "unexpected error creating temp wal")

	blockID := uuid.New()
	tenantID := "fake"

	block, err := wal.NewBlock(blockID, tenantID)
	assert.NoError(t, err, "unexpected error creating block")

	numMsgs := 10
	reqs := make([]*friggpb.PushRequest, 0, numMsgs)
	for i := 0; i < numMsgs; i++ {
		req := test.MakeRequest(rand.Int()%1000, []byte{})
		reqs = append(reqs, req)
		err := block.Write([]byte{}, req)
		assert.NoError(t, err, "unexpected error writing req")
	}

	outReq := &friggpb.PushRequest{}
	i := 0
	err = block.(*headBlock).Iterator(outReq, func(msg proto.Message) (bool, error) {
		req := msg.(*friggpb.PushRequest)

		assert.True(t, proto.Equal(req, reqs[i]))
		i++

		return true, nil
	})

	assert.NoError(t, err, "unexpected error iterating")
	assert.Equal(t, numMsgs, i)
}

func TestCompleteBlock(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	wal, err := newWAL(&walConfig{
		filepath:        tempDir,
		indexDownsample: 2,
	})
	assert.NoError(t, err, "unexpected error creating temp wal")

	blockID := uuid.New()
	tenantID := "fake"

	block, err := wal.NewBlock(blockID, tenantID)
	assert.NoError(t, err, "unexpected error creating block")

	numMsgs := 10
	reqs := make([]*friggpb.PushRequest, 0, numMsgs)
	for i := 0; i < numMsgs; i++ {
		req := test.MakeRequest(rand.Int()%1000, []byte{})
		reqs = append(reqs, req)
		err := block.Write([]byte{}, req)
		assert.NoError(t, err, "unexpected error writing req")
	}

	complete, err := block.Complete(wal)
	assert.NoError(t, err, "unexpected error completing block")

	outReq := &friggpb.PushRequest{}
	i := 0
	err = complete.Iterator(outReq, func(msg proto.Message) (bool, error) {
		req := msg.(*friggpb.PushRequest)

		assert.True(t, proto.Equal(req, reqs[i]))
		i++

		return true, nil
	})

	// confirm order
	var prev *Record
	for _, r := range complete.(*headBlock).records {
		if prev != nil {
			assert.Greater(t, r.Start, prev.Start)
		}

		prev = r
	}

	assert.NoError(t, err, "unexpected error iterating")
	assert.Equal(t, numMsgs, i)
}

func TestWorkDir(t *testing.T) {
	tempDir, err := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)
	assert.NoError(t, err, "unexpected error creating temp dir")

	err = os.MkdirAll(path.Join(tempDir, workDir), os.ModePerm)
	assert.NoError(t, err, "unexpected error creating workdir")

	_, err = os.Create(path.Join(tempDir, workDir, "testfile"))
	assert.NoError(t, err, "unexpected error creating testfile")

	_, err = newWAL(&walConfig{
		filepath:        tempDir,
		indexDownsample: 2,
	})
	assert.NoError(t, err, "unexpected error creating temp wal")

	_, err = os.Stat(path.Join(tempDir, workDir))
	assert.NoError(t, err, "work folder should exist")

	files, err := ioutil.ReadDir(path.Join(tempDir, workDir))
	assert.NoError(t, err, "unexpected reading work dir")

	assert.Len(t, files, 0, "work dir should be empty")
}

func BenchmarkWriteRead(b *testing.B) {
	tempDir, _ := ioutil.TempDir("/tmp", "")
	defer os.RemoveAll(tempDir)

	wal, _ := newWAL(&walConfig{
		filepath:        tempDir,
		indexDownsample: 2,
	})

	blockID := uuid.New()
	tenantID := "fake"

	// 1 million requests, 10k spans per request
	block, _ := wal.NewBlock(blockID, tenantID)
	numMsgs := 100
	reqs := make([]*friggpb.PushRequest, 0, numMsgs)
	for i := 0; i < numMsgs; i++ {
		req := test.MakeRequest(100, []byte{})
		reqs = append(reqs, req)
	}

	outReq := &friggpb.PushRequest{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, req := range reqs {
			block.Write(req.Batch.Spans[0].TraceId, req)
			block.Find(req.Batch.Spans[0].TraceId, outReq)
		}
	}
}