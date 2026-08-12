-- Back to the three kinds a pre-000034 build knows.
--
-- Existing datasheet conversations would violate the narrower constraint, so
-- their subject is cleared first. The conversations themselves are kept: what
-- was asked and what it cost is worth more than which page it was asked from.
UPDATE assistant_conversations SET subject_kind = NULL, subject_id = NULL
    WHERE subject_kind = 'datasheet';

ALTER TABLE assistant_conversations
    DROP CONSTRAINT assistant_conversations_subject_kind_check;

ALTER TABLE assistant_conversations
    ADD CONSTRAINT assistant_conversations_subject_kind_check
    CHECK (subject_kind IN ('part', 'project', 'board'));
