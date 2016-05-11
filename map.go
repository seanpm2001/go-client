/*
   Copyright 2016 Continusec Pty Ltd

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package continusec

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type mapHashResponse struct {
	MapHash []byte            `json:"map_hash"`
	LogSTH  *treeSizeResponse `json:"mutation_log"`
}

// VerifiableMap is an object used to interact with Verifiable Maps. To construct this
// object, call NewClient(...).VerifiableMap("mapname")
type VerifiableMap struct {
	client *Client
	path   string
}

// MutationLog returns a pointer to the underlying Verifiable Log that represents
// a log of mutations to this map. Since this Verifiable Log is managed by this map,
// the log returned cannot be directly added to (to mutate, call Set and Delete methods
// on the map), however all read-only functions are present.
func (self *VerifiableMap) MutationLog() *VerifiableLog {
	return &VerifiableLog{
		client: self.client,
		path:   self.path + "/log/mutation",
	}
}

// TreeHeadLog returns a pointer to the underlying Verifiable Log that represents
// a log of tree heads generated by this map. Since this Verifiable Map is managed by this map,
// the log returned cannot be directly added to however all read-only functions are present.
func (self *VerifiableMap) TreeHeadLog() *VerifiableLog {
	return &VerifiableLog{
		client: self.client,
		path:   self.path + "/log/treehead",
	}
}

// Create will send an API call to create a new map with the name specified when the
// VerifiableMap object was instantiated.
func (self *VerifiableMap) Create() error {
	_, _, err := self.client.makeRequest("PUT", self.path, nil)
	if err != nil {
		return err
	}
	return nil
}

func parseHeadersForProof(headers http.Header) ([][]byte, error) {
	prv := make([][]byte, 256)
	actualHeaders, ok := headers[http.CanonicalHeaderKey("X-Verified-Proof")]
	if ok {
		for _, h := range actualHeaders {
			for _, commad := range strings.Split(h, ",") {
				bits := strings.SplitN(commad, "/", 2)
				if len(bits) == 2 {
					idx, err := strconv.Atoi(strings.TrimSpace(bits[0]))
					if err != nil {
						return nil, err
					}
					bs, err := hex.DecodeString(strings.TrimSpace(bits[1]))
					if err != nil {
						return nil, err
					}
					if idx < 256 {
						prv[idx] = bs
					}
				}
			}
		}
	}
	return prv, nil
}

// Get will return the value for the given key at the given treeSize. Pass continusec.Head
// to always get the latest value. factory is normally one of RawDataEntryFactory, JsonEntryFactory or RedactedJsonEntryFactory.
func (self *VerifiableMap) Get(key []byte, treeSize int64, factory VerifiableEntryFactory) (*MapInclusionProof, error) {
	value, headers, err := self.client.makeRequest("GET", self.path+fmt.Sprintf("/tree/%d/key/h/%s%s", treeSize, hex.EncodeToString(key), factory.Format()), nil)
	if err != nil {
		return nil, err
	}

	prv, err := parseHeadersForProof(headers)
	if err != nil {
		return nil, err
	}

	rv, err := factory.CreateFromBytes(value)
	if err != nil {
		return nil, err
	}

	vts, err := strconv.Atoi(headers.Get("X-Verified-TreeSize"))
	if err != nil {
		return nil, err
	}

	return &MapInclusionProof{
		Value:     rv,
		TreeSize:  int64(vts),
		AuditPath: prv,
		Key:       key,
	}, nil
}

// Set will set generate a map mutation to set the given value for the given key.
// While this will return quickly, the change will be reflected asynchronously in the map.
func (self *VerifiableMap) Set(key []byte, value UploadableEntry) error {
	data, err := value.DataForUpload()
	if err != nil {
		return err
	}
	_, _, err = self.client.makeRequest("PUT", self.path+"/key/h/"+hex.EncodeToString(key)+value.Format(), data)
	if err != nil {
		return err
	}
	return nil
}

// Delete will set generate a map mutation to delete the value for the given key. Calling Delete
// is equivalent to calling Set with an empty value.
// While this will return quickly, the change will be reflected asynchronously in the map.
func (self *VerifiableMap) Delete(key []byte) error {
	_, _, err := self.client.makeRequest("DELETE", self.path+"/key/h/"+hex.EncodeToString(key), nil)
	if err != nil {
		return err
	}
	return nil
}

// TreeHash returns map root hash for the map at the given tree size. Specify continusec.Head
// to receive a root hash for the latest tree size.
func (self *VerifiableMap) TreeHead(treeSize int64) (*MapTreeHead, error) {
	contents, _, err := self.client.makeRequest("GET", self.path+fmt.Sprintf("/tree/%d", treeSize), nil)
	if err != nil {
		return nil, err
	}
	var cr mapHashResponse
	err = json.Unmarshal(contents, &cr)
	if err != nil {
		return nil, err
	}
	return &MapTreeHead{
		RootHash: cr.MapHash,
		MutationLogTreeHead: LogTreeHead{
			TreeSize: cr.LogSTH.TreeSize,
			RootHash: cr.LogSTH.Hash,
		},
	}, nil
}

// BlockUntilSize blocks until the map has caught up to a certain size. This polls
// getTreeHead(int) until such time as a new tree hash is produced that is of at least this
// size. This is intended for test use.
func (self *VerifiableMap) BlockUntilSize(treeSize int64) (*MapTreeHead, error) {
	lastHead := int64(-1)
	timeToSleep := time.Second
	for {
		lth, err := self.TreeHead(Head)
		if err != nil {
			return nil, err
		}
		if lth.MutationLogTreeHead.TreeSize >= treeSize {
			return lth, nil
		} else {
			if lth.MutationLogTreeHead.TreeSize > lastHead {
				lastHead = lth.MutationLogTreeHead.TreeSize
				// since we got a new tree head, reset sleep time
				timeToSleep = time.Second
			} else {
				// no luck, snooze a bit longer
				timeToSleep *= 2
			}
			time.Sleep(timeToSleep)
		}
	}
}
