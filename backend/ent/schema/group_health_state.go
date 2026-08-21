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

// GroupHealthState is the durable health snapshot used by dynamic routing.
type GroupHealthState struct{ ent.Schema }

func (GroupHealthState) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "group_health_states"}}
}
func (GroupHealthState) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (GroupHealthState) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id"),
		field.String("status").MaxLen(20).Default("unknown"),
		field.String("reason").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("last_probe_at").Optional().Nillable(),
		field.Time("last_success_at").Optional().Nillable(),
		field.Time("next_probe_at").Optional().Nillable(),
		field.Int("failure_count").Default(0),
		field.Int("probe_ttft_ms").Default(0),
		field.Float("probe_availability_6h").SchemaType(map[string]string{dialect.Postgres: "decimal(7,4)"}).Default(0),
		field.Int("probe_ttft_avg_ms").Default(0),
		field.Int("probe_ttft_p95_ms").Default(0),
		field.Int("probe_samples").Default(0),
		field.Int("real_ttft_p50_ms").Default(0),
		field.Int("real_ttft_avg_ms").Default(0),
		field.Int("real_ttft_p95_ms").Default(0),
		field.Int("real_ttft_samples").Default(0),
		field.Float("real_availability_6h").SchemaType(map[string]string{dialect.Postgres: "decimal(7,4)"}).Default(0),
		field.Int("real_total_avg_ms").Default(0),
	}
}
func (GroupHealthState) Edges() []ent.Edge {
	return []ent.Edge{edge.From("group", Group.Type).Ref("health_state").Field("group_id").Unique().Required()}
}
func (GroupHealthState) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id").Unique(), index.Fields("status"), index.Fields("next_probe_at")}
}
