CREATE TABLE IF NOT EXISTS workspace_business_projects (
    id VARCHAR(64) NOT NULL,
    name VARCHAR(255) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS workspace_products (
    id VARCHAR(64) NOT NULL,
    business_project_id VARCHAR(64) NOT NULL,
    name VARCHAR(255) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (id),
    KEY workspace_products_parent (business_project_id),
    FOREIGN KEY (business_project_id) REFERENCES workspace_business_projects (id) ON DELETE NO ACTION
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS workspace_applications (
    application_id VARCHAR(64) NOT NULL,
    product_id VARCHAR(64),
    revision BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (application_id),
    KEY workspace_applications_product (product_id),
    FOREIGN KEY (application_id) REFERENCES projects (id) ON DELETE NO ACTION,
    FOREIGN KEY (product_id) REFERENCES workspace_products (id) ON DELETE NO ACTION
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
