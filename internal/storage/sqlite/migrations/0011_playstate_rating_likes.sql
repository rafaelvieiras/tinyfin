-- Per-user rating and like/dislike flag on an item, written through
-- POST /UserItems/{itemId}/UserData. Both are optional members of the user
-- data object: NULL means "never set" and is omitted from responses.
ALTER TABLE playstates ADD COLUMN rating REAL;
ALTER TABLE playstates ADD COLUMN likes INTEGER;
