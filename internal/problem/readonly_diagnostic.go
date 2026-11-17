package problem

import "errors"

func prepareReadOnlyDiagnostic(in *ReadOnlyContextDiagnostic) (*ReadOnlyContextDiagnostic, error) {
	if in == nil {
		return nil, nil
	}
	copy := *in
	switch copy.Status {
	case "available", "missing", "unavailable":
	default:
		return nil, errors.New("invalid read-only context diagnostic status")
	}
	switch copy.Stage {
	case "repository_task", "root", "directory", "driver", "association":
	default:
		return nil, errors.New("invalid read-only context diagnostic stage")
	}
	switch copy.Reason {
	case "observed_driver", "no_matching_driver", "unsupported_task", "missing_task_identifier", "invalid_path", "depth_limit", "directory_limit", "invalid_parent", "missing_trees", "invalid_entry", "filename_mismatch", "missing_content", "invalid_content", "content_size_mismatch", "unsupported_content", "content_limit", "invalid_response", "unavailable_http", "unavailable_upstream", "unavailable_timeout", "unavailable_network", "unavailable_cancelled":
	default:
		return nil, errors.New("invalid read-only context diagnostic reason")
	}
	if (copy.Status == "available") != (copy.Reason == "observed_driver") || (copy.Status == "missing") != (copy.Reason == "no_matching_driver") {
		return nil, errors.New("inconsistent read-only context diagnostic")
	}
	return &copy, nil
}
