CREATE VIEW IF NOT EXISTS minted_ecash (amount) AS SELECT COALESCE((SELECT SUM(amount) FROM mint_quotes WHERE state = 'ISSUED'), 0);
CREATE VIEW IF NOT EXISTS melted_ecash (amount) AS SELECT COALESCE((SELECT SUM(amount) FROM melt_quotes WHERE state = 'PAID'), 0);
CREATE VIEW IF NOT EXISTS balance (balance) AS SELECT (SELECT amount FROM minted_ecash) - (SELECT amount FROM melted_ecash);
