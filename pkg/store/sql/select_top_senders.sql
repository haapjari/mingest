-- Top Senders: a walk of the covering sender_top index, about ten entries
-- however many Senders there are.
SELECT address, messages FROM sender ORDER BY messages DESC, address LIMIT ?;
