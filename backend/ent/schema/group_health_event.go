package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

// GroupHealthEvent stores probe and real-request observations for auditing and rolling metrics.
type GroupHealthEvent struct{ ent.Schema }

func (GroupHealthEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "group_health_events"}}
}
func (GroupHealthEvent) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (GroupHealthEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id"),
		field.Int64("account_id").Optional().Nillable(),
		field.String("kind").MaxLen(20).Default("probe"),
		field.Bool("success").Default(false),
		field.Bool("is_probe").Default(true),
		field.Bool("semantic_started").Default(false),
		field.String("error_category").MaxLen(50).Optional().Nillable(),
		field.Int("ttft_ms").Default(0),
		field.Int("total_ms").Default(0),
		field.Time("observed_at"),
		field.String("error_message").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}
func (GroupHealthEvent) Edges() []ent.Edge {
	return []ent.Edge{edge.From("group", Group.Type).Ref("health_events").Field("group_id").Unique().Required()}
}
func (GroupHealthEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id", "observed_at"), index.Fields("is_probe", "observed_at")}
}
