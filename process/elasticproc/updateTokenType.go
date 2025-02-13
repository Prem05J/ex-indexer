package elasticproc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TerraDharitri/drt-go-chain-core/core"
	"github.com/TerraDharitri/drt-go-chain-es-indexer/core/request"
	"github.com/TerraDharitri/drt-go-chain-es-indexer/data"
	elasticIndexer "github.com/TerraDharitri/drt-go-chain-es-indexer/process/dataindexer"
)

func (ei *elasticProcessor) indexTokens(tokensData []*data.TokenInfo, updateNFTData []*data.NFTDataUpdate, buffSlice *data.BufferSlice, shardID uint32) error {
	err := ei.prepareAndAddSerializedDataForTokens(tokensData, updateNFTData, buffSlice, elasticIndexer.DCDTsIndex)
	if err != nil {
		return err
	}
	err = ei.prepareAndAddSerializedDataForTokens(tokensData, updateNFTData, buffSlice, elasticIndexer.TokensIndex)
	if err != nil {
		return err
	}

	err = ei.addTokenType(tokensData, elasticIndexer.AccountsDCDTIndex, shardID)
	if err != nil {
		return err
	}

	return ei.addTokenType(tokensData, elasticIndexer.TokensIndex, shardID)
}

func (ei *elasticProcessor) prepareAndAddSerializedDataForTokens(tokensData []*data.TokenInfo, updateNFTData []*data.NFTDataUpdate, buffSlice *data.BufferSlice, index string) error {
	if !ei.isIndexEnabled(index) {
		return nil
	}

	return ei.logsAndEventsProc.SerializeTokens(tokensData, updateNFTData, buffSlice, index)
}

func (ei *elasticProcessor) addTokenType(tokensData []*data.TokenInfo, index string, shardID uint32) error {
	if len(tokensData) == 0 {
		return nil
	}

	defer func(startTime time.Time) {
		log.Debug("elasticProcessor.addTokenType", "index", index, "duration", time.Since(startTime))
	}(time.Now())

	for _, td := range tokensData {
		if td.Type == core.FungibleDCDT {
			continue
		}

		handlerFunc := func(responseBytes []byte) error {
			responseScroll := &data.ResponseScroll{}
			err := json.Unmarshal(responseBytes, responseScroll)
			if err != nil {
				return err
			}

			ids := make([]string, 0, len(responseScroll.Hits.Hits))
			for _, res := range responseScroll.Hits.Hits {
				ids = append(ids, res.ID)
			}

			buffSlice := data.NewBufferSlice(ei.bulkRequestMaxSize)
			err = ei.accountsProc.SerializeTypeForProvidedIDs(ids, td.Type, buffSlice, index)
			if err != nil {
				return err
			}

			return ei.doBulkRequests(index, buffSlice.Buffers(), shardID)
		}

		ctxWithValue := context.WithValue(context.Background(), request.ContextKey, request.ExtendTopicWithShardID(request.GetTopic, shardID))
		query := fmt.Sprintf(`{"query": {"bool": {"must": [{"match": {"token": {"query": "%s","operator": "AND"}}}],"must_not":[{"exists": {"field": "type"}}]}}}`, td.Token)
		resultsCount, err := ei.elasticClient.DoCountRequest(ctxWithValue, index, []byte(query))
		if err != nil || resultsCount == 0 {
			return err
		}

		ctxWithValue = context.WithValue(context.Background(), request.ContextKey, request.ExtendTopicWithShardID(request.ScrollTopic, shardID))
		err = ei.elasticClient.DoScrollRequest(ctxWithValue, index, []byte(query), false, handlerFunc)
		if err != nil {
			return err
		}
	}

	return nil
}
