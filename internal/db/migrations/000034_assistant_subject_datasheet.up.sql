-- Let a conversation be about a datasheet.
--
-- The datasheet viewer became a page (/datasheets/:id), and the assistant popup
-- works out what you are looking at from the route. So it started sending
-- subject_kind = 'datasheet', which this CHECK rejected, and every question
-- asked on a datasheet page failed with a 500 before it ever reached the model.
--
-- The list has to be edited rather than appended to: a CHECK constraint is
-- replaced, not extended.
--
-- READ THIS BEFORE ADDING A SUBJECT KIND: this column is a free string in the
-- request struct (handlers.sendMessageRequest) and in the web
-- (AssistantPopup.subjectFor). Nothing but this constraint stops a new kind
-- reaching the INSERT, so teaching the web a new subject WITHOUT a migration
-- here breaks the assistant on that page. The handler now also drops an
-- unrecognised kind rather than passing it through, so the failure degrades to
-- "a conversation with no subject" instead of a 500 — but the kind still has to
-- be added here for the subject to actually be recorded.
ALTER TABLE assistant_conversations
    DROP CONSTRAINT assistant_conversations_subject_kind_check;

ALTER TABLE assistant_conversations
    ADD CONSTRAINT assistant_conversations_subject_kind_check
    CHECK (subject_kind IN ('part', 'project', 'board', 'datasheet'));
