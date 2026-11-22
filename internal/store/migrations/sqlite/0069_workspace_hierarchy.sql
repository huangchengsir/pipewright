CREATE TABLE IF NOT EXISTS workspace_business_projects (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (id)
);

CREATE TABLE IF NOT EXISTS workspace_products (
    id TEXT NOT NULL,
    business_project_id TEXT NOT NULL,
    name TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (id),
    FOREIGN KEY (business_project_id) REFERENCES workspace_business_projects (id) ON DELETE NO ACTION
);

CREATE TABLE IF NOT EXISTS workspace_applications (
    application_id TEXT NOT NULL,
    product_id TEXT,
    revision INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (application_id),
    FOREIGN KEY (application_id) REFERENCES projects (id) ON DELETE NO ACTION,
    FOREIGN KEY (product_id) REFERENCES workspace_products (id) ON DELETE NO ACTION
);

CREATE INDEX IF NOT EXISTS workspace_products_parent ON workspace_products (business_project_id);

CREATE INDEX IF NOT EXISTS workspace_applications_product ON workspace_applications (product_id);
