package store

import "strings"

// PointedTypes are the types whose governing flow a pointer in the store
// answers for, "" being the depot's own: a pointer that leads to a flow of
// the repository counts, since it stands in front of the type's file all the
// same. A type's file in the repository that no pointer of its own answers for
// is read by nobody once the depot's own pointer is in the store, which wins
// as a whole (resolveFlow).
func PointedTypes(flows []DepotFlow) map[string]bool {
	pointed := map[string]bool{}
	for _, df := range flows {
		pointed[df.Type] = df.Source.Kind == "store"
	}
	return pointed
}

// ShadowedRepoFlow is the file of the depot's repository that df, a flow in
// the store, stands in front of: default.flow for the depot's own flow, and
// for a type's the type's file, else the named flow the type's flow was copied
// from (flow copy-repo copies yardr.train as yardr.train.local, planCopyItem).
// It is nil where df is a repository flow, where repo is nil and where the
// repository has no such file. A file that does not parse is answered all the
// same, with its Error, since it is still shadowed.
func ShadowedRepoFlow(repo *RepoFlows, df DepotFlow) *RepoFlow {
	if repo == nil || !df.Source.InStore() {
		return nil
	}
	if rf := repo.find("default", df.Type); rf != nil || df.Type == "" {
		return rf
	}
	return repo.find(strings.TrimSuffix(df.Flow.Name, ".local"), "")
}

// FlowDiffersFromRepo says whether the store flow df differs from the
// repository files it shadows, by what both hold now, and for which of its
// stages the stage file does. The yard keeps no record of what a copy was made
// from, so a difference does not say which side moved: the yard's copy was
// edited, or the repository changed since.
//
// The flow differs where its canonical text does (Flow.String, which leaves
// the name out), where the stage file of one of its stages does (byte for byte,
// a stage with no file and one with nothing in it counting as the same, as
// whoever reads the stage has it), where the shadowed file does not parse, and,
// for the depot's own flow, where the repository has a type's file that no
// pointer answers for (pointed, from PointedTypes): it landed after the copy
// and nothing reads it. A stage only the repository's flow has would make the
// canonical texts differ already, so the stages walked are the store flow's. A
// repository flow, and a store flow that shadows no file, differ from nothing.
//
// home is the yard's home, where a store flow's stage files are, and repo the
// one the caller read already, so the comparison runs no git of its own.
func FlowDiffersFromRepo(home string, df DepotFlow, repo *RepoFlows, pointed map[string]bool) (flowDiffers bool, stageDiffers map[string]bool, err error) {
	if repo == nil || !df.Source.InStore() {
		return false, nil, nil
	}
	if df.Type == "" {
		for _, rf := range repo.Flows {
			if rf.Type != "" && !pointed[rf.Type] {
				flowDiffers = true
			}
		}
	}
	rf := ShadowedRepoFlow(repo, df)
	switch {
	case rf == nil:
		return flowDiffers, nil, nil
	case rf.Error != "" || rf.Flow == nil:
		return true, nil, nil
	}
	flowDiffers = flowDiffers || df.Flow.String() != rf.Flow.String()
	stageDiffers = map[string]bool{}
	for _, stage := range df.Flow.Stages {
		stored, err := HomeStageFile(home, df.Flow.Name, stage)
		if err != nil {
			return false, nil, err
		}
		var homeText, repoText string
		if stored != nil {
			homeText = stored.Text
		}
		if sf := repo.StageFile(rf.Flow.Name, stage); sf != nil {
			repoText = sf.Text
		}
		if homeText != repoText {
			stageDiffers[stage] = true
			flowDiffers = true
		}
	}
	return flowDiffers, stageDiffers, nil
}
