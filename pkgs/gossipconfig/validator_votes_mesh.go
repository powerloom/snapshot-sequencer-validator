package gossipconfig

import (
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ValidatorVotesGossipParams returns the gossipsub parameters optimized
// for the validator votes mesh. These parameters will be tuned for
// reliable vote propagation once validator consensus is implemented.
// Topics: /powerloom/validator-votes/*, /powerloom/consensus/*
func ValidatorVotesGossipParams() *pubsub.GossipSubParams {
	// Start with snapshot submissions parameters as baseline
	params := SnapshotSubmissionsGossipParams()
	
	// TODO: Tune these parameters specifically for validator vote propagation
	// Considerations for future tuning:
	// - Validators need extremely reliable message delivery
	// - Vote messages are time-sensitive
	// - May need different D parameters for smaller validator sets
	// - Consider faster heartbeat for vote synchronization
	
	return params
}

// ValidatorVotesPeerScoreParams returns peer scoring parameters
// optimized for the validator votes mesh.
func ValidatorVotesPeerScoreParams(hostID peer.ID) *pubsub.PeerScoreParams {
	// Start with snapshot submissions scoring as baseline
	scoreParams := SnapshotSubmissionsPeerScoreParams(hostID)
	
	// TODO: Adjust scoring for validator-specific requirements
	// Considerations:
	// - Validators should have higher baseline scores
	// - May need to penalize invalid votes more strictly
	// - Consider reputation-based scoring from on-chain stake
	
	return scoreParams
}

// ValidatorVotesTopicScoreParams returns topic scoring parameters
// optimized for validator vote topics.
func ValidatorVotesTopicScoreParams() *pubsub.TopicScoreParams {
	// Start with snapshot submissions topic scoring
	params := SnapshotSubmissionsTopicScoreParams()
	
	// Validator-specific adjustments for vote reliability
	params.FirstMessageDeliveriesWeight = 200.0 // Even higher weight for vote delivery
	params.InvalidMessageDeliveriesWeight = -200.0 // Stricter penalty for invalid votes
	
	// TODO: Further tuning once validator consensus is implemented
	// - May need to adjust TimeInMesh parameters
	// - Consider different decay rates for vote topics
	// - Possibly enable some mesh delivery requirements
	
	return params
}

// ValidatorVotesPeerScoreThresholds returns score thresholds
// for the validator votes mesh.
func ValidatorVotesPeerScoreThresholds() *pubsub.PeerScoreThresholds {
	// Start with lenient thresholds similar to snapshot submissions
	thresholds := SnapshotSubmissionsPeerScoreThresholds()
	
	// TODO: Adjust thresholds based on validator network size
	// - Smaller validator sets may need different thresholds
	// - Consider stake-weighted scoring in the future
	
	return thresholds
}

// ConfigureValidatorVotesMesh configures a pubsub instance with parameters
// optimized for the validator votes mesh. This will be used by validator nodes
// participating in consensus.
func ConfigureValidatorVotesMesh(hostID peer.ID, validatorTopics []string) (*pubsub.GossipSubParams, *pubsub.PeerScoreParams, *pubsub.PeerScoreThresholds) {
	// Get validator votes mesh parameters
	gossipParams := ValidatorVotesGossipParams()
	peerScoreParams := ValidatorVotesPeerScoreParams(hostID)
	peerScoreThresholds := ValidatorVotesPeerScoreThresholds()

	// Configure topic score parameters for validator topics
	topicScoreParams := ValidatorVotesTopicScoreParams()
	
	// Set scoring for provided validator topics
	for _, topic := range validatorTopics {
		peerScoreParams.Topics[topic] = topicScoreParams
	}
	
	// Common validator topics (to be expanded)
	peerScoreParams.Topics["/powerloom/consensus/votes"] = topicScoreParams
	peerScoreParams.Topics["/powerloom/consensus/proposals"] = topicScoreParams

	return gossipParams, peerScoreParams, peerScoreThresholds
}

// Example future usage for validators:
//
// validatorTopics := []string{
//     "/powerloom/consensus/votes",
//     "/powerloom/consensus/proposals",
//     "/powerloom/validator-votes/epoch-1234",
// }
// 
// gossipParams, peerScoreParams, peerScoreThresholds := gossipconfig.ConfigureValidatorVotesMesh(host.ID(), validatorTopics)
// 
// ps, err := pubsub.NewGossipSub(
//     ctx,
//     host,
//     pubsub.WithGossipSubParams(gossipParams),
//     pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
//     pubsub.WithFloodPublish(true),
// )