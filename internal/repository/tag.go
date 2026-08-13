// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/firelabsca/firebin-api/internal/models"
	"github.com/firelabsca/firebin-api/internal/tags"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalid is returned when an argument cannot describe a tag at all: a name
// that folds to an empty slug, or a merge of a tag into itself. Distinct from
// ErrConflict, which means the request was well formed and lost a race with an
// existing row.
var ErrInvalid = errors.New("invalid")

type TagRepo struct{ pool *pgxpool.Pool }

func NewTagRepo(pool *pgxpool.Pool) *TagRepo { return &TagRepo{pool: pool} }

const tagCols = `tags.id, tags.name, tags.slug, tags.colour, tags.description, tags.created_at, tags.updated_at`

// TagColours are the palette slots a tag may use. Slot names, not hex values:
// the web app maps each to a CSS variable, so a tag stays legible in whichever
// theme the viewer is running. Anything outside this set is rejected.
var TagColours = []string{"slate", "red", "amber", "green", "teal", "blue", "violet", "pink"}

// ValidTagColour reports whether c names a palette slot.
func ValidTagColour(c string) bool {
	for _, v := range TagColours {
		if v == c {
			return true
		}
	}
	return false
}

// TagSlug folds a tag name to its identity. See tags.Slug for the rule and why
// it is what it is; this is an alias so callers already reaching for the
// repository do not have to import a second package to fold a name, and so
// there is exactly one definition of what makes two spellings the same tag.
func TagSlug(name string) string { return tags.Slug(name) }

func scanTag(row pgx.Row) (models.Tag, error) {
	var t models.Tag
	err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.Colour, &t.Description, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// List returns every tag with the number of parts carrying it, by name.
func (r *TagRepo) List(ctx context.Context) ([]models.Tag, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+tagCols+`,
		(SELECT COUNT(*) FROM part_tags pt WHERE pt.tag_id = tags.id)::int AS part_count
		FROM tags ORDER BY tags.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.Tag, 0)
	for rows.Next() {
		var t models.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Colour, &t.Description,
			&t.CreatedAt, &t.UpdatedAt, &t.PartCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get returns one tag by id.
func (r *TagRepo) Get(ctx context.Context, id uuid.UUID) (models.Tag, error) {
	t, err := scanTag(r.pool.QueryRow(ctx, `SELECT `+tagCols+` FROM tags WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Create inserts a tag. A name that folds to an existing tag's slug yields
// ErrConflict, so the caller can point at the tag that already covers it rather
// than quietly creating a second spelling of the same word.
func (r *TagRepo) Create(ctx context.Context, name string, colour, description *string) (models.Tag, error) {
	slug := TagSlug(name)
	if slug == "" {
		return models.Tag{}, ErrInvalid
	}
	t, err := scanTag(r.pool.QueryRow(ctx,
		`INSERT INTO tags (name, slug, colour, description) VALUES ($1,$2,$3,$4) RETURNING `+tagCols,
		strings.TrimSpace(name), slug, colour, description))
	if isUniqueViolation(err) {
		return t, ErrConflict
	}
	return t, err
}

// Update renames or recolours a tag. A nil field is left alone.
//
// A rename whose new name folds onto another tag's slug is ErrConflict, never a
// silent merge: "Qwiic" and "qwiic" collapsing on a typo would move every part
// on one of them with no way back. Merge is a separate, deliberate call.
func (r *TagRepo) Update(ctx context.Context, id uuid.UUID, name, colour, description *string) (models.Tag, error) {
	sets := []string{}
	args := []any{id}
	if name != nil {
		slug := TagSlug(*name)
		if slug == "" {
			return models.Tag{}, ErrInvalid
		}
		args = append(args, strings.TrimSpace(*name))
		sets = append(sets, `name = $`+itoa(len(args)))
		args = append(args, slug)
		sets = append(sets, `slug = $`+itoa(len(args)))
	}
	if colour != nil {
		// An empty string clears the colour back to the default chip.
		var c *string
		if *colour != "" {
			c = colour
		}
		args = append(args, c)
		sets = append(sets, `colour = $`+itoa(len(args)))
	}
	if description != nil {
		var d *string
		if *description != "" {
			d = description
		}
		args = append(args, d)
		sets = append(sets, `description = $`+itoa(len(args)))
	}
	if len(sets) == 0 {
		return r.Get(ctx, id)
	}
	t, err := scanTag(r.pool.QueryRow(ctx,
		`UPDATE tags SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+tagCols, args...))
	if isUniqueViolation(err) {
		return t, ErrConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Delete removes a tag and, by cascade, every part's link to it.
func (r *TagRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM tags WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Merge moves every part carrying `from` onto `into`, then deletes `from`.
// This is how two spellings that escaped into the wild get reconciled without
// visiting each part.
//
// Parts already carrying both survive the move (ON CONFLICT DO NOTHING) rather
// than failing the merge on the composite key.
func (r *TagRepo) Merge(ctx context.Context, from, into uuid.UUID) error {
	if from == into {
		return ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tags WHERE id = $1)`, into).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `INSERT INTO part_tags (part_id, tag_id)
		SELECT part_id, $2 FROM part_tags WHERE tag_id = $1
		ON CONFLICT DO NOTHING`, from, into); err != nil {
		return err
	}
	ct, err := tx.Exec(ctx, `DELETE FROM tags WHERE id = $1`, from)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// Resolve turns a list of typed names into tags, creating the ones that do not
// exist yet. Names that fold to the same slug collapse to one tag, and names
// that fold to nothing (punctuation only) are dropped.
//
// Insert-then-select rather than ON CONFLICT DO UPDATE ... RETURNING: the
// update form would touch updated_at on every existing tag every time a part is
// saved, so a tag you have not edited in months would look freshly changed.
func (r *TagRepo) Resolve(ctx context.Context, names []string) ([]models.Tag, error) {
	type pair struct{ name, slug string }
	seen := map[string]bool{}
	wanted := []pair{}
	for _, n := range names {
		slug := TagSlug(n)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		wanted = append(wanted, pair{strings.TrimSpace(n), slug})
	}
	if len(wanted) == 0 {
		return []models.Tag{}, nil
	}

	insNames := make([]string, len(wanted))
	slugs := make([]string, len(wanted))
	for i, w := range wanted {
		insNames[i] = w.name
		slugs[i] = w.slug
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `INSERT INTO tags (name, slug)
		SELECT * FROM unnest($1::text[], $2::text[])
		ON CONFLICT (slug) DO NOTHING`, insNames, slugs); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+tagCols+` FROM tags WHERE slug = ANY($1) ORDER BY tags.name`, slugs)
	if err != nil {
		return nil, err
	}
	out := make([]models.Tag, 0, len(wanted))
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

// SetPartTags replaces a part's tags with exactly these names, creating any tag
// that does not exist yet, and returns the resulting set.
func (r *TagRepo) SetPartTags(ctx context.Context, partID uuid.UUID, names []string) ([]models.Tag, error) {
	tags, err := r.Resolve(ctx, names)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(tags))
	for i, t := range tags {
		ids[i] = t.ID
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM part_tags WHERE part_id = $1 AND NOT (tag_id = ANY($2))`, partID, ids); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO part_tags (part_id, tag_id)
			SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, partID, ids); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return tags, nil
}

// AddPartTags puts these names on a part without disturbing the tags already
// there, and returns the part's full resulting set.
//
// Add and remove exist alongside SetPartTags because a caller working from a
// name ("call this one qwiic") does not know the rest of the set, and a
// full-replace from such a caller silently drops every tag it had not heard of.
func (r *TagRepo) AddPartTags(ctx context.Context, partID uuid.UUID, names []string) ([]models.Tag, error) {
	tags, err := r.Resolve(ctx, names)
	if err != nil {
		return nil, err
	}
	if len(tags) > 0 {
		ids := make([]uuid.UUID, len(tags))
		for i, t := range tags {
			ids[i] = t.ID
		}
		if _, err := r.pool.Exec(ctx, `INSERT INTO part_tags (part_id, tag_id)
			SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, partID, ids); err != nil {
			return nil, err
		}
	}
	return r.TagsForPart(ctx, partID)
}

// RemovePartTags takes these names off a part, leaving the tags themselves in
// the vocabulary, and returns the part's remaining set. A name the part does not
// carry is not an error.
func (r *TagRepo) RemovePartTags(ctx context.Context, partID uuid.UUID, names []string) ([]models.Tag, error) {
	slugs := make([]string, 0, len(names))
	for _, n := range names {
		if s := TagSlug(n); s != "" {
			slugs = append(slugs, s)
		}
	}
	if len(slugs) > 0 {
		if _, err := r.pool.Exec(ctx, `DELETE FROM part_tags pt
			USING tags t WHERE t.id = pt.tag_id AND pt.part_id = $1 AND t.slug = ANY($2)`,
			partID, slugs); err != nil {
			return nil, err
		}
	}
	return r.TagsForPart(ctx, partID)
}

// TagsForPart returns one part's tags, by name.
func (r *TagRepo) TagsForPart(ctx context.Context, partID uuid.UUID) ([]models.Tag, error) {
	byPart, err := r.TagsForParts(ctx, []uuid.UUID{partID})
	if err != nil {
		return nil, err
	}
	if t, ok := byPart[partID]; ok {
		return t, nil
	}
	return []models.Tag{}, nil
}

// TagsForParts returns the tags of many parts in one round trip, keyed by part.
//
// Batched deliberately. The alternative — a json_agg in the parts list query —
// would mean another column on partCols, and partCols is scanned by four
// hand-written scan sites that a new column does not force you to update; it
// compiles, vets clean, and 500s at runtime. Keeping tags out of that select
// list means none of those four sites move.
func (r *TagRepo) TagsForParts(ctx context.Context, partIDs []uuid.UUID) (map[uuid.UUID][]models.Tag, error) {
	out := make(map[uuid.UUID][]models.Tag, len(partIDs))
	if len(partIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT pt.part_id, `+tagCols+`
		FROM part_tags pt JOIN tags ON tags.id = pt.tag_id
		WHERE pt.part_id = ANY($1) ORDER BY tags.name`, partIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var partID uuid.UUID
		var t models.Tag
		if err := rows.Scan(&partID, &t.ID, &t.Name, &t.Slug, &t.Colour, &t.Description,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out[partID] = append(out[partID], t)
	}
	return out, rows.Err()
}

// PartsWithTag returns the ids of parts carrying a tag, by slug. Used by BOM
// matching, which needs to know not just that a tag matched but whether it
// matched exactly one part.
func (r *TagRepo) PartsWithTag(ctx context.Context, slug string) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT pt.part_id FROM part_tags pt
		JOIN tags t ON t.id = pt.tag_id WHERE t.slug = $1`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
