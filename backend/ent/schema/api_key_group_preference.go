package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

// APIKeyGroupPreference stores per-key disabled groups and custom ordering.
type APIKeyGroupPreference struct{ ent.Schema }

func (APIKeyGroupPreference) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "api_key_group_preferences"}}
}
func (APIKeyGroupPreference) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (APIKeyGroupPreference) Fields() []ent.Field {
	return []ent.Field{field.Int64("api_key_id"), field.Int64("group_id"), field.Bool("disabled").Default(false), field.Int("position").Default(0)}
}
func (APIKeyGroupPreference) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("api_key", APIKey.Type).Ref("group_preferences").Field("api_key_id").Unique().Required(),
		edge.From("group", Group.Type).Ref("key_preferences").Field("group_id").Unique().Required(),
	}
}
func (APIKeyGroupPreference) Indexes() []ent.Index {
	return []ent.Index{index.Fields("api_key_id", "group_id").Unique(), index.Fields("api_key_id", "position")}
}
