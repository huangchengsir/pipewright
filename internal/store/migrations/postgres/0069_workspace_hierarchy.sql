CREATE TABLE IF NOT EXISTS workspace_business_projects (
    id TEXT COLLATE "C" NOT NULL,
    name TEXT COLLATE "C" NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (id)
);

CREATE TABLE IF NOT EXISTS workspace_products (
    id TEXT COLLATE "C" NOT NULL,
    business_project_id TEXT COLLATE "C" NOT NULL,
    name TEXT COLLATE "C" NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (id),
    FOREIGN KEY (business_project_id) REFERENCES workspace_business_projects (id) ON DELETE NO ACTION
);

CREATE TABLE IF NOT EXISTS workspace_applications (
    application_id TEXT COLLATE "C" NOT NULL,
    product_id TEXT COLLATE "C",
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (application_id),
    FOREIGN KEY (application_id) REFERENCES projects (id) ON DELETE NO ACTION,
    FOREIGN KEY (product_id) REFERENCES workspace_products (id) ON DELETE NO ACTION
);

CREATE INDEX IF NOT EXISTS workspace_products_parent ON workspace_products (business_project_id);

CREATE INDEX IF NOT EXISTS workspace_applications_product ON workspace_applications (product_id);
