// Copyright 2017 Attic Labs, Inc. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

package nbs

import (
	"os"
	"testing"

	"github.com/attic-labs/noms/go/util/sizecache"
	"github.com/attic-labs/testify/assert"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

func TestDynamoTableReader(t *testing.T) {
	ddb := makeFakeDDB(t)

	chunks := [][]byte{
		[]byte("hello2"),
		[]byte("goodbye2"),
		[]byte("badbye2"),
	}

	tableData, h := buildTable(chunks)
	ddb.putData(fmtTableName(h), tableData)

	t.Run("DynamoGet", func(t *testing.T) {
		test := func(ddb ddbsvc) {
			assert := assert.New(t)
			data, err := tryDynamoTableRead(ddb, "table", h)
			assert.NoError(err)
			assert.Equal(tableData, data)

			data, err = tryDynamoTableRead(ddb, "table", computeAddr([]byte{}))
			assert.Error(err)
			assert.IsType(tableNotInDynamoErr{}, err)
			assert.Nil(data)
		}

		t.Run("EventuallyConsistentSuccess", func(t *testing.T) {
			test(ddb)
		})

		t.Run("EventuallyConsistentFailure", func(t *testing.T) {
			test(&eventuallyConsistentDDB{ddb})
		})
	})

	t.Run("NoIndexCache", func(t *testing.T) {
		trc := newDynamoTableReader(ddb, "table", h, uint32(len(chunks)), tableData, nil, nil)
		assertChunksInReader(chunks, trc, assert.New(t))
	})

	t.Run("WithIndexCache", func(t *testing.T) {
		assert := assert.New(t)
		index := parseTableIndex(tableData)
		cache := newIndexCache(1024)
		cache.put(h, index)

		baseline := ddb.numGets
		trc := newDynamoTableReader(ddb, "table", h, uint32(len(chunks)), tableData, cache, nil)

		// constructing the table reader shouldn't have resulted in any reads
		assert.Zero(ddb.numGets - baseline)
		assertChunksInReader(chunks, trc, assert)
	})

	t.Run("WithTableCache", func(t *testing.T) {
		assert := assert.New(t)
		dir := makeTempDir(t)
		defer os.RemoveAll(dir)
		stats := &Stats{}

		tc := sizecache.New(uint64(2 * len(tableData)))
		trc := newDynamoTableReader(ddb, "table", h, uint32(len(chunks)), tableData, nil, tc)
		tra := trc.(tableReaderAt)

		// First, read when table is not yet cached
		scratch := make([]byte, len(tableData)/4)
		baseline := ddb.numGets
		_, err := tra.ReadAtWithStats(scratch, 0, stats)
		assert.NoError(err)
		assert.True(ddb.numGets > baseline)

		// Table should have been cached on read so read again, a different slice this time
		baseline = ddb.numGets
		_, err = tra.ReadAtWithStats(scratch, int64(len(scratch)), stats)
		assert.NoError(err)
		assert.Zero(ddb.numGets - baseline)
	})
}

type eventuallyConsistentDDB struct {
	ddbsvc
}

func (ec *eventuallyConsistentDDB) GetItem(input *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
	if input.ConsistentRead != nil && *(input.ConsistentRead) {
		return ec.ddbsvc.GetItem(input)
	}
	return &dynamodb.GetItemOutput{}, nil
}
