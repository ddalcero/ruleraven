package mongo

import "go.mongodb.org/mongo-driver/mongo/options"

var optionsUpdateUpsert = options.Update().SetUpsert(true)
