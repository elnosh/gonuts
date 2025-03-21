-- drop previous balance views
DROP VIEW minted_ecash;
DROP VIEW melted_ecash;
DROP VIEW balance;

-- create new balance views by keyset
CREATE VIEW IF NOT EXISTS total_issued AS 
SELECT keyset_id, COALESCE(amount, 0) AS balance FROM (
    SELECT keyset_id, SUM(amount) AS amount 
    FROM blind_signatures 
    GROUP BY keyset_id
);

CREATE VIEW IF NOT EXISTS total_redeemed AS 
SELECT keyset_id, COALESCE(amount, 0) AS balance FROM (
    SELECT keyset_id, SUM(amount) AS amount 
    FROM proofs 
    GROUP BY keyset_id
);
