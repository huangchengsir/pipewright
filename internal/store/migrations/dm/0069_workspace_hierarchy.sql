CREATE TABLE IF NOT EXISTS workspace_business_projects (
    id VARCHAR(64 CHAR) NOT NULL,
    name VARCHAR(255 CHAR) NOT NULL,
    revision BIGINT DEFAULT 1 NOT NULL,
    NOT CLUSTER PRIMARY KEY (id)
) STORAGE(USING LONG ROW);
-- pipewright:statement
CREATE TABLE IF NOT EXISTS workspace_products (
    id VARCHAR(64 CHAR) NOT NULL,
    business_project_id VARCHAR(64 CHAR) NOT NULL,
    name VARCHAR(255 CHAR) NOT NULL,
    revision BIGINT DEFAULT 1 NOT NULL,
    NOT CLUSTER PRIMARY KEY (id),
    FOREIGN KEY (business_project_id) REFERENCES workspace_business_projects (id)
) STORAGE(USING LONG ROW);
-- pipewright:statement
CREATE TABLE IF NOT EXISTS workspace_applications (
    application_id VARCHAR(64 CHAR) NOT NULL,
    product_id VARCHAR(64 CHAR),
    revision BIGINT DEFAULT 1 NOT NULL,
    NOT CLUSTER PRIMARY KEY (application_id),
    FOREIGN KEY (application_id) REFERENCES projects (id),
    FOREIGN KEY (product_id) REFERENCES workspace_products (id)
) STORAGE(USING LONG ROW);
